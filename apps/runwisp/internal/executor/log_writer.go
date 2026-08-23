// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
)

const (
	// defaultDiskCheckInterval is how many bytes between disk-space probes.
	defaultDiskCheckInterval int64 = 10 * 1024 * 1024 // 10 MB
)

// LogWriter writes log output to a plain-text `.log` file, accompanied by a
// single hidden sidecar container (see logutil.MetaPath) that holds the line
// index, rotation metadata and progress-bar frame history.
//
// The container is created lazily: nothing is written to it until the run
// produces something worth recording — the segment crosses LogIndexInterval
// lines (1024), an in-place frame group settles, or the run closes (final
// metadata). A short ten-line cron run therefore leaves just its `.log` plus a
// container holding a single metadata record, and a plain `ls` of the log
// directory shows only the `.log`.
type LogWriter struct {
	mu       sync.Mutex
	mainPath string
	metaPath string // consolidated sidecar container (hidden)

	file     *os.File
	metaFile *os.File // nil until the first sidecar record is appended

	// indexStarted reports whether the current segment has begun recording the
	// line index (i.e. crossed LogIndexInterval lines and backfilled the chunk-0
	// entry). Reset to false on rotation.
	indexStarted bool

	// Size limiting
	maxSize    int64  // 0 = unlimited
	overflow   string // model.LogOverflow* constants
	cancelFunc func() // cancel context for kill mode

	// Disk space monitoring
	minFreeDisk       int64  // 0 = disabled
	logDir            string // for statfs calls
	lastDiskCheck     int64  // offset at last disk check
	diskCheckInterval int64  // bytes between probes; defaults to defaultDiskCheckInterval
	diskPressureHit   bool   // set once after the first threshold crossing
	onDiskPressure    func(free, min int64, killedTask bool)
	freeDisk          func(dir string) int64 // injected for tests; defaults to freeDiskSpace

	// State
	currentOffset  int64
	lineCount      int64
	totalProduced  int64 // total bytes the process output (before any truncation)
	truncated      bool
	stopped        bool // drop_new mode or disk-full: no more writes accepted
	killedByPolicy bool // kill overflow tripped — distinguishes policy-kill from clean stop

	// Rotation bookkeeping: cumulative counts from rotated-away files
	rotatedLines int64
	rotatedBytes int64
	// prevSegmentStart is the global line number the segment currently sitting in
	// the .prev slot started at (rotatedLines just before the most recent
	// rotation). Persisted so readers number .prev correctly after 2+ rotations.
	prevSegmentStart int64

	now func() time.Time // injected wall-clock; never call time.Now() inline
}

// LogWriterOpts configures a LogWriter.
type LogWriterOpts struct {
	LogPath     string
	MaxSize     int64  // 0 = unlimited
	Overflow    string // model.LogOverflow* constants; defaults to drop_old
	CancelFunc  func() // for kill mode (log_max_size and disk-pressure)
	MinFreeDisk int64  // 0 = disabled
	LogDir      string
	// OnDiskPressure fires once per writer when min_free_space first trips.
	// killedTask reports whether the writer also cancelled the task because
	// Overflow == kill. Invoked synchronously under the writer's mutex,
	// so the callback must not block.
	OnDiskPressure func(free, min int64, killedTask bool)
	// Now is the wall-clock source for system lines. Required (must not be nil);
	// production wiring passes time.Now.
	Now func() time.Time
}

func NewLogWriter(opts LogWriterOpts) (*LogWriter, error) {
	f, err := os.Create(opts.LogPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}

	overflow := opts.Overflow
	if overflow == "" {
		overflow = model.LogOverflowDropOld
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}

	// The sidecar container is created lazily. Make sure no stale container or
	// rotated-away segment from a prior run lingers at the same paths.
	metaPath := logutil.MetaPath(opts.LogPath)
	_ = os.Remove(metaPath)
	_ = os.Remove(logutil.PrevPath(opts.LogPath))

	return &LogWriter{
		mainPath:          opts.LogPath,
		metaPath:          metaPath,
		file:              f,
		maxSize:           opts.MaxSize,
		overflow:          overflow,
		cancelFunc:        opts.CancelFunc,
		minFreeDisk:       opts.MinFreeDisk,
		logDir:            opts.LogDir,
		onDiskPressure:    opts.OnDiskPressure,
		diskCheckInterval: defaultDiskCheckInterval,
		freeDisk:          freeDiskSpace,
		now:               now,
	}, nil
}

// WriteLineEvent appends a single line for the given stream and returns the
// absolute line number assigned by the writer (rotated_lines + line_count).
// Returns -1 when the line is silently dropped (writer stopped, drop_new,
// kill, disk-low). The returned number is monotonically non-decreasing
// across rotations and is the canonical `n` for log events on the bus.
func (w *LogWriter) WriteLineEvent(text, stream string) (int64, error) {
	formatted := []byte(logutil.FormatLine(text, stream))
	lineNum, _, err := w.writeOneLine(formatted)
	return lineNum, err
}

// WriteFrameHistory appends the whole-region frame history for a settled commit
// group, keyed to the group's first committed line number (anchor). The sidecar
// container is opened lazily on the first call, so logs without any in-place
// output never create one. Best-effort and supplementary: a write failure is
// returned but never affects the durable log.
func (w *LogWriter) WriteFrameHistory(anchor int64, frames [][]string) error {
	if len(frames) == 0 {
		return nil
	}
	rec, err := logutil.FrameRecord(anchor, frames)
	if err != nil {
		return fmt.Errorf("encode frame history: %w", err)
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.stopped {
		return nil
	}
	if !w.ensureContainer() {
		return fmt.Errorf("create sidecar container")
	}
	if _, err := w.metaFile.Write(rec); err != nil {
		return fmt.Errorf("write frame history: %w", err)
	}
	return nil
}

// writeOneLine is the single mutex-guarded write path. Returns the absolute
// line number assigned (or -1 if the line was dropped) plus the io.Writer
// `n` (always len(p) for drop cases, matching the prior contract).
func (w *LogWriter) writeOneLine(p []byte) (lineNum int64, written int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.totalProduced += int64(len(p))

	if w.stopped {
		return -1, len(p), nil
	}

	if w.checkDiskPressure() {
		return -1, len(p), nil
	}

	if w.handleSizeOverflow(p) {
		return -1, len(p), nil
	}

	// Capture the assigned line number AFTER any rotation (which may have
	// reset lineCount and bumped rotatedLines), BEFORE the write+increment.
	assignedN := w.rotatedLines + w.lineCount

	n, err := w.file.Write(p)
	if err != nil {
		return -1, n, err
	}

	if n > 0 {
		w.updateSidecarsAfterWrite()
		w.currentOffset += int64(n)
		w.lineCount++
	}

	return assignedN, n, nil
}

// checkDiskPressure periodically samples free disk space and stops the writer
// when it drops below minFreeDisk. Fires OnDiskPressure once per writer
// instance on the first threshold crossing. Must be called with mu held.
// Returns true when the caller should drop the current write.
func (w *LogWriter) checkDiskPressure() bool {
	if w.minFreeDisk <= 0 || w.currentOffset-w.lastDiskCheck <= w.diskCheckInterval {
		return false
	}
	w.lastDiskCheck = w.currentOffset
	free := w.freeDisk(w.logDir)
	if free < 0 || free >= w.minFreeDisk {
		return false
	}
	killed := w.overflow == model.LogOverflowKill
	if killed {
		w.writeSystemLine(fmt.Sprintf(
			"Process killed: disk space critically low (%s free, minimum %s required); log_on_full=\"kill\".",
			config.FormatByteSize(free), config.FormatByteSize(w.minFreeDisk)))
	} else {
		w.writeSystemLine(fmt.Sprintf(
			"Log output stopped: disk space critically low (%s free, minimum %s required).",
			config.FormatByteSize(free), config.FormatByteSize(w.minFreeDisk)))
	}
	w.stopped = true
	w.truncated = true
	if killed && w.cancelFunc != nil {
		w.cancelFunc()
	}
	if !w.diskPressureHit {
		w.diskPressureHit = true
		if w.onDiskPressure != nil {
			w.onDiskPressure(free, w.minFreeDisk, killed)
		}
	}
	return true
}

// handleSizeOverflow checks whether writing p would exceed the per-run maxSize
// cap and enforces the configured overflow policy. Must be called with mu held.
// Returns true when the caller should drop the current write.
func (w *LogWriter) handleSizeOverflow(p []byte) bool {
	if w.maxSize <= 0 || w.currentOffset+int64(len(p)) <= w.maxSize {
		return false
	}
	switch w.overflow {
	case model.LogOverflowDropNew:
		w.writeSystemLine(fmt.Sprintf(
			"Log output truncated: exceeded log_max_size (%s). Process continues running.",
			config.FormatByteSize(w.maxSize)))
		w.stopped = true
		w.truncated = true
		return true
	case model.LogOverflowKill:
		w.writeSystemLine(fmt.Sprintf(
			"Process killed: log output exceeded log_max_size (%s).",
			config.FormatByteSize(w.maxSize)))
		w.truncated = true
		w.stopped = true
		w.killedByPolicy = true
		if w.cancelFunc != nil {
			w.cancelFunc()
		}
		return true
	case model.LogOverflowDropOld:
		if err := w.rotateTail(); err != nil {
			slog.Error("Failed to rotate log file, continuing without rotation", "err", err)
		}
	}
	return false
}

// ensureContainer lazily opens the sidecar container for appending. Returns
// false (logging once) when the container cannot be created; callers then skip
// the record — sidecar data is best-effort and its loss never affects the log.
// Caller must hold mu.
func (w *LogWriter) ensureContainer() bool {
	if w.metaFile != nil {
		return true
	}
	f, err := os.OpenFile(w.metaPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		slog.Warn("Failed to create log sidecar container", "err", err)
		return false
	}
	w.metaFile = f
	return true
}

// appendRecord appends one framed sidecar record, opening the container if
// needed. Best-effort: failures are logged and swallowed. Caller must hold mu.
func (w *LogWriter) appendRecord(rec []byte) {
	if len(rec) == 0 || !w.ensureContainer() {
		return
	}
	if _, err := w.metaFile.Write(rec); err != nil {
		slog.Warn("Failed to write log sidecar record", "err", err)
	}
}

// updateSidecarsAfterWrite records line-index entries following a successful
// line write. Must be called with mu held, before lineCount is incremented.
func (w *LogWriter) updateSidecarsAfterWrite() {
	// Index policy: record a chunk offset only once the segment has produced a
	// full chunk. Below that, the container holds no index records. The first
	// crossing backfills the chunk-0 index entry.
	if w.lineCount > 0 && w.lineCount%logutil.LogIndexInterval == 0 {
		w.ensureIndexBackfill()
		if w.indexStarted {
			w.appendRecord(logutil.IndexRecord(w.currentOffset))
		}
	}
}

// ensureIndexBackfill writes the chunk-0 index record the first time the segment
// crosses LogIndexInterval lines, so readers can still locate line 0.
// Subsequent calls are no-ops. Caller must hold mu.
func (w *LogWriter) ensureIndexBackfill() {
	if w.indexStarted {
		return
	}
	// Chunk-0 index: line 0 sits at offset 0 in the segment.
	w.appendRecord(logutil.IndexRecord(0))
	w.indexStarted = true
}

func (w *LogWriter) writeSystemLine(msg string) {
	line := fmt.Sprintf("[%s] [SYSTEM] %s\n",
		w.now().Format("2006-01-02 15:04:05"), msg)
	n, err := w.file.Write([]byte(line))
	if err != nil {
		slog.Warn("Failed to write system log line", "err", err)
	}
	w.currentOffset += int64(n)
	w.lineCount++
}

// rotateTail rotates the current log file to keep only the most recent output.
// The rotated-away segment is renamed to its hidden .prev slot. The sidecar
// container is rewritten to preserve run-scoped frame history while resetting
// the segment-scoped line index; the new segment re-records its index lazily if
// and when it crosses the threshold. Caller must hold mu.
func (w *LogWriter) rotateTail() error {
	// The segment about to move into .prev started at the current cumulative
	// count; capture it before folding this segment's lines into the total.
	w.prevSegmentStart = w.rotatedLines
	w.rotatedLines += w.lineCount
	w.rotatedBytes += w.currentOffset

	if err := w.file.Sync(); err != nil {
		slog.Warn("Failed to sync log file before rotation", "err", err)
	}
	if err := w.file.Close(); err != nil {
		slog.Warn("Failed to close log file before rotation", "err", err)
	}

	prevPath := logutil.PrevPath(w.mainPath)
	os.Remove(prevPath) // drop a previous rotation artifact

	if err := os.Rename(w.mainPath, prevPath); err != nil {
		// Undo accumulation and re-open the original log for appending.
		w.rotatedLines -= w.lineCount
		w.rotatedBytes -= w.currentOffset
		var reopenErr error
		w.file, reopenErr = os.OpenFile(w.mainPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if reopenErr != nil {
			w.stopped = true
			slog.Error("Failed to re-open log file after rotation failure", "err", reopenErr)
		}
		return fmt.Errorf("failed to rotate log: %w", err)
	}

	f, err := os.Create(w.mainPath)
	if err != nil {
		return fmt.Errorf("failed to create new log file after rotation: %w", err)
	}
	w.file = f
	w.currentOffset = 0
	w.lineCount = 0
	// currentOffset just reset to 0; lastDiskCheck tracks it, so reset too or the
	// (currentOffset - lastDiskCheck) throttle stays negative forever and disk-
	// pressure checks never fire again for the rest of the run.
	w.lastDiskCheck = 0
	w.truncated = true

	// Rewrite the container: keep frame history, reset the index, persist the
	// new rotation counts so readers can compute virtual offsets.
	if err := w.rewriteContainerForRotation(); err != nil {
		slog.Warn("Failed to rewrite log sidecar after rotation", "err", err)
	}

	w.writeSystemLine(fmt.Sprintf(
		"Log rotated: output exceeded logs.maxSize (%s). Earlier output truncated.",
		config.FormatByteSize(w.maxSize)))

	return nil
}

// rewriteContainerForRotation truncates the sidecar container and rewrites it
// with the run-scoped frame history (which spans the whole run) plus a fresh
// rotation-metadata record. Segment-scoped index records are dropped; the new
// segment re-records them lazily. This keeps the container from growing
// without bound across repeated rotations. Caller must hold mu.
func (w *LogWriter) rewriteContainerForRotation() error {
	frames := logutil.ReadSidecar(w.mainPath).Frames

	if w.metaFile != nil {
		w.metaFile.Sync()
		w.metaFile.Close()
		w.metaFile = nil
	}
	w.indexStarted = false

	f, err := os.Create(w.metaPath) // truncates
	if err != nil {
		return fmt.Errorf("recreate sidecar container: %w", err)
	}
	w.metaFile = f

	for anchor, fr := range frames {
		if rec, encErr := logutil.FrameRecord(anchor, fr); encErr == nil {
			w.metaFile.Write(rec)
		}
	}
	w.metaFile.Write(logutil.MetaRecord(logutil.LogMeta{
		RotatedLines: w.rotatedLines,
		RotatedBytes: w.rotatedBytes,
		PrevStart:    w.prevSegmentStart,
	}))
	return nil
}

func (w *LogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.writeTotalProducedLine()

	// Persist finalized metadata so readers never need to scan the file. This
	// also creates the container for short runs that produced no index/frames.
	w.appendRecord(logutil.MetaRecord(logutil.LogMeta{
		RotatedLines: w.rotatedLines,
		RotatedBytes: w.rotatedBytes,
		FinalLines:   w.lineCount,
		Finalized:    true,
	}))

	var errs []error
	if w.file != nil {
		w.file.Sync()
		if err := w.file.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if w.metaFile != nil {
		w.metaFile.Sync()
		if err := w.metaFile.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	// The `.prev` segment (if any) is NOT removed here: retention already
	// accounts for its size and deletes it when the run itself is purged
	// (see internal/runtime/retention.go, softdelete_purger.go). Deleting it
	// on every Close would destroy the pre-rotation output of a run the
	// instant it ends, before an operator can ever view or download it.

	return errors.Join(errs...)
}

// writeTotalProducedLine appends a system summary of total bytes produced when
// any output was truncated. Caller must hold mu.
func (w *LogWriter) writeTotalProducedLine() {
	if !w.truncated {
		return
	}
	if w.maxSize > 0 {
		w.writeSystemLine(fmt.Sprintf(
			"Total process output: %s (limit: %s).",
			config.FormatByteSize(w.totalProduced), config.FormatByteSize(w.maxSize)))
	} else {
		w.writeSystemLine(fmt.Sprintf(
			"Total process output: %s.",
			config.FormatByteSize(w.totalProduced)))
	}
}

// KilledByPolicy reports whether the writer cancelled the run because the
// task's log_on_full = "kill" policy tripped. Distinguishes a
// policy-enforced kill from an operator-initiated stop.
func (w *LogWriter) KilledByPolicy() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.killedByPolicy
}

// freeDiskSpace returns available bytes on the filesystem containing path,
// or -1 if the check fails.
func freeDiskSpace(path string) int64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return -1
	}
	return int64(stat.Bavail) * int64(stat.Bsize)
}
