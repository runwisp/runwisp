// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package executor

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"log/slog"
)

const (
	// defaultDiskCheckInterval is how many bytes between disk-space probes.
	defaultDiskCheckInterval int64 = 10 * 1024 * 1024 // 10 MB
)

// LogWriter writes log output to a file with an accompanying binary index,
// enforcing optional per-run size limits with configurable overflow behaviour.
//
// Index sidecars (.idx, .tidx) are created lazily: nothing is written to disk
// for them until the current segment crosses LogIndexInterval lines (1024).
// Short runs leave no sidecar files, which keeps the on-disk footprint of
// the average ten-line cron run minimal.
type LogWriter struct {
	mu       sync.Mutex
	mainPath string
	idxPath  string
	tidxPath string

	file     *os.File
	idxFile  *os.File // nil until segment crosses LogIndexInterval lines
	tidxFile *os.File // nil until segment crosses LogIndexInterval lines

	// Size limiting
	maxSize    int64  // 0 = unlimited
	overflow   string // model.LogOverflow* constants
	cancelFunc func() // cancel context for kill_task mode

	// Disk space monitoring
	minFreeDisk       int64  // 0 = disabled
	logDir            string // for statfs calls
	lastDiskCheck     int64  // offset at last disk check
	diskCheckInterval int64  // bytes between probes; defaults to defaultDiskCheckInterval
	diskPressureHit   bool   // set once after the first threshold crossing
	onDiskPressure    func(free, min int64, killedTask bool)

	// State
	currentOffset  int64
	lineCount      int64
	totalProduced  int64 // total bytes the process output (before any truncation)
	truncated      bool
	stopped        bool // drop_new mode or disk-full: no more writes accepted
	killedByPolicy bool // kill_task overflow tripped — distinguishes policy-kill from clean stop

	// Rotation bookkeeping: cumulative counts from rotated-away files
	rotatedLines int64
	rotatedBytes int64

	// Timestamp index
	firstWriteMs int64            // unix ms of the first line in the segment, for lazy tidx backfill
	lastTidxTime int64            // unix ms of last tidx entry
	now          func() time.Time // injected wall-clock; never call time.Now() inline
	nowMs        func() int64     // unix-ms view of now, derived in the constructor
}

// LogWriterOpts configures a LogWriter.
type LogWriterOpts struct {
	LogPath     string
	IdxPath     string
	TidxPath    string // timestamp index path (.tidx)
	MaxSize     int64  // 0 = unlimited
	Overflow    string // model.LogOverflow* constants; defaults to drop_old
	CancelFunc  func() // for kill_task mode (log_max_size and disk-pressure)
	MinFreeDisk int64  // 0 = disabled
	LogDir      string
	// OnDiskPressure fires once per writer when min_free_space first trips.
	// killedTask reports whether the writer also cancelled the task because
	// Overflow == kill_task. Invoked synchronously under the writer's mutex,
	// so the callback must not block.
	OnDiskPressure func(free, min int64, killedTask bool)
	// DiskCheckInterval overrides the default 10MB probe interval. 0 = default.
	DiskCheckInterval int64
	// Now is the wall-clock source for system lines and the timestamp index.
	// Required (must not be nil); production wiring passes time.Now.
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

	interval := opts.DiskCheckInterval
	if interval <= 0 {
		interval = defaultDiskCheckInterval
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}

	// idx and tidx sidecars are not opened here. They appear on disk only
	// after the segment crosses LogIndexInterval lines, which is when an
	// index is actually useful. Make sure no stale file from a prior run
	// lingers at the same path.
	_ = os.Remove(opts.IdxPath)
	if opts.TidxPath != "" {
		_ = os.Remove(opts.TidxPath)
	}

	return &LogWriter{
		mainPath:          opts.LogPath,
		idxPath:           opts.IdxPath,
		tidxPath:          opts.TidxPath,
		file:              f,
		maxSize:           opts.MaxSize,
		overflow:          overflow,
		cancelFunc:        opts.CancelFunc,
		minFreeDisk:       opts.MinFreeDisk,
		logDir:            opts.LogDir,
		onDiskPressure:    opts.OnDiskPressure,
		diskCheckInterval: interval,
		now:               now,
		nowMs:             func() int64 { return now().UnixMilli() },
	}, nil
}

// Write satisfies io.Writer. Each call must contain exactly one
// '\n'-terminated line; the writer assigns one absolute line number per call.
// Returns len(p) for silently-dropped lines (drop_new / kill_task / disk-low)
// to keep upstream pipes from backing up.
func (w *LogWriter) Write(p []byte) (n int, err error) {
	_, written, err := w.writeOneLine(p)
	return written, err
}

// WriteLineEvent appends a single line for the given stream and returns the
// absolute line number assigned by the writer (rotated_lines + line_count).
// Returns -1 when the line is silently dropped (writer stopped, drop_new,
// kill_task, disk-low). The returned number is monotonically non-decreasing
// across rotations and is the canonical `n` for log events on the bus.
func (w *LogWriter) WriteLineEvent(text, stream string) (int64, error) {
	formatted := []byte(logutil.FormatLine(text, stream))
	lineNum, _, err := w.writeOneLine(formatted)
	return lineNum, err
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

	// Periodic disk-space check. The check honours the task's log_on_full
	// policy: kill_task tasks die when disk is critical (the operator chose
	// loud failure over disk safety); other policies just stop logging. In
	// both cases we fire OnDiskPressure once so the operator is notified —
	// silent disk-pressure stop is a Prime Directive violation.
	if w.minFreeDisk > 0 && w.currentOffset-w.lastDiskCheck > w.diskCheckInterval {
		w.lastDiskCheck = w.currentOffset
		if free := freeDiskSpace(w.logDir); free >= 0 && free < w.minFreeDisk {
			killed := w.overflow == model.LogOverflowKillTask
			if killed {
				w.writeSystemLine(fmt.Sprintf(
					"Process killed: disk space critically low (%s free, minimum %s required); log_on_full=\"kill_task\".",
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
			return -1, len(p), nil
		}
	}

	// Check per-run size limit
	if w.maxSize > 0 && w.currentOffset+int64(len(p)) > w.maxSize {
		switch w.overflow {
		case model.LogOverflowDropNew:
			w.writeSystemLine(fmt.Sprintf(
				"Log output truncated: exceeded log_max_size (%s). Process continues running.",
				config.FormatByteSize(w.maxSize)))
			w.stopped = true
			w.truncated = true
			return -1, len(p), nil

		case model.LogOverflowKillTask:
			w.writeSystemLine(fmt.Sprintf(
				"Process killed: log output exceeded log_max_size (%s).",
				config.FormatByteSize(w.maxSize)))
			w.truncated = true
			w.stopped = true
			w.killedByPolicy = true
			if w.cancelFunc != nil {
				w.cancelFunc()
			}
			return -1, len(p), nil

		case model.LogOverflowDropOld:
			if rotateErr := w.rotateTail(); rotateErr != nil {
				slog.Error("Failed to rotate log file, continuing without rotation", "err", rotateErr)
			}
		}
	}

	// Capture the assigned line number AFTER any rotation (which may have
	// reset lineCount and bumped rotatedLines), BEFORE the write+increment.
	assignedN := w.rotatedLines + w.lineCount

	n, err := w.file.Write(p)
	if err != nil {
		return -1, n, err
	}

	if n > 0 {
		if w.firstWriteMs == 0 {
			w.firstWriteMs = w.nowMs()
		}
		// Sidecar policy: write an idx entry only once the segment has
		// produced at least one full chunk. Below that, the sidecar files
		// don't exist on disk at all. The first time we cross the threshold
		// we lazily open both files and backfill chunk-0 entries.
		if w.lineCount > 0 && w.lineCount%logutil.LogIndexInterval == 0 {
			w.ensureIndexSidecars()
			if w.idxFile != nil {
				var buf [8]byte
				binary.LittleEndian.PutUint64(buf[:], uint64(w.currentOffset))
				if _, idxErr := w.idxFile.Write(buf[:]); idxErr != nil {
					slog.Warn("Failed to write to log index", "err", idxErr)
				}
			}
		}
		w.writeTidxEntry()
		w.currentOffset += int64(n)
		w.lineCount++
	}

	return assignedN, n, nil
}

// ensureIndexSidecars opens the .idx and (if configured) .tidx files on the
// first call past LogIndexInterval lines, writing the chunk-0 backfill
// entries so readers can still locate line 0. Subsequent calls are no-ops.
// Caller must hold w.mu.
func (w *LogWriter) ensureIndexSidecars() {
	if w.idxFile != nil {
		return
	}
	idx, err := os.Create(w.idxPath)
	if err != nil {
		slog.Warn("Failed to lazy-create log index", "err", err)
		return
	}
	w.idxFile = idx
	// Backfill chunk-0: line 0 sits at offset 0 in the segment.
	var idxEntry [8]byte
	binary.LittleEndian.PutUint64(idxEntry[:], 0)
	if _, err := w.idxFile.Write(idxEntry[:]); err != nil {
		slog.Warn("Failed to write initial log index entry", "err", err)
	}
	if w.tidxPath == "" {
		return
	}
	tidx, err := os.Create(w.tidxPath)
	if err != nil {
		slog.Warn("Failed to lazy-create timestamp index", "err", err)
		return
	}
	w.tidxFile = tidx
	// Backfill the chunk-0 timestamp entry. firstWriteMs is set on the first
	// successful write to the segment, so the recovered line-0 timestamp
	// matches the run's actual start within a few milliseconds even when the
	// sidecar opens much later.
	ts := w.firstWriteMs
	if ts == 0 {
		ts = w.nowMs()
	}
	var tidxEntry [logutil.TimestampIndexEntrySize]byte
	logutil.WriteTimestampEntry(tidxEntry[:], logutil.TimestampEntry{
		Line:      0,
		Timestamp: ts,
	})
	if _, err := w.tidxFile.Write(tidxEntry[:]); err != nil {
		slog.Warn("Failed to write initial timestamp index entry", "err", err)
	}
	w.lastTidxTime = ts
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

// writeTidxEntry appends a timestamp index entry if the dual trigger fires:
// every LogIndexInterval lines OR every 1 second since last entry.
// Caller must hold mu. Called before lineCount is incremented.
func (w *LogWriter) writeTidxEntry() {
	if w.tidxFile == nil || w.lineCount > math.MaxUint32 {
		return
	}
	now := w.nowMs()
	if now < w.lastTidxTime {
		now = w.lastTidxTime // enforce monotonicity against clock adjustments
	}
	if w.lineCount%logutil.LogIndexInterval == 0 || now-w.lastTidxTime >= 1000 {
		var buf [logutil.TimestampIndexEntrySize]byte
		logutil.WriteTimestampEntry(buf[:], logutil.TimestampEntry{
			Line:      uint32(w.lineCount),
			Timestamp: now,
		})
		if _, err := w.tidxFile.Write(buf[:]); err != nil {
			slog.Warn("Failed to write to timestamp index", "err", err)
		}
		w.lastTidxTime = now
	}
}

// rotateTail rotates the current log file to keep only the most recent output.
// Sidecar files for the rotating-away segment are kept only if they were
// lazy-opened (i.e. the segment crossed the index threshold). The new
// segment starts fresh with no sidecars; they will be lazy-opened if and
// when it crosses the threshold itself. Caller must hold mu.
func (w *LogWriter) rotateTail() error {
	// Accumulate counts from the file we are about to rotate away
	w.rotatedLines += w.lineCount
	w.rotatedBytes += w.currentOffset

	if err := w.file.Sync(); err != nil {
		slog.Warn("Failed to sync log file before rotation", "err", err)
	}
	if err := w.file.Close(); err != nil {
		slog.Warn("Failed to close log file before rotation", "err", err)
	}
	if w.idxFile != nil {
		if err := w.idxFile.Sync(); err != nil {
			slog.Warn("Failed to sync log index before rotation", "err", err)
		}
		if err := w.idxFile.Close(); err != nil {
			slog.Warn("Failed to close log index before rotation", "err", err)
		}
	}
	if w.tidxFile != nil {
		w.tidxFile.Sync()
		w.tidxFile.Close()
	}

	prevPath := w.mainPath + ".prev"
	prevIdxPath := w.idxPath + ".prev"
	prevTidxPath := ""
	if w.tidxPath != "" {
		prevTidxPath = w.tidxPath + ".prev"
	}

	// Delete previous rotation artifact
	os.Remove(prevPath)
	os.Remove(prevIdxPath)
	if prevTidxPath != "" {
		os.Remove(prevTidxPath)
	}

	if err := os.Rename(w.mainPath, prevPath); err != nil {
		// Undo accumulation on failure
		w.rotatedLines -= w.lineCount
		w.rotatedBytes -= w.currentOffset
		var reopenErr error
		w.file, reopenErr = os.OpenFile(w.mainPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if reopenErr != nil {
			w.stopped = true
			slog.Error("Failed to re-open log file after rotation failure", "err", reopenErr)
		}
		// idx/tidx are lazy now; nothing to reopen on the failure path. The
		// segment will recreate them itself if it crosses the threshold.
		w.idxFile = nil
		w.tidxFile = nil
		return fmt.Errorf("failed to rotate log: %w", err)
	}
	if w.idxFile != nil {
		os.Rename(w.idxPath, prevIdxPath) // best-effort; stale idx is harmless
	} else {
		// No sidecar in the rotating segment — make sure there's no stale
		// .prev lingering from an earlier rotation cycle.
		os.Remove(prevIdxPath)
	}
	if w.tidxPath != "" {
		if w.tidxFile != nil {
			os.Rename(w.tidxPath, prevTidxPath)
		} else {
			os.Remove(prevTidxPath)
		}
	}

	f, err := os.Create(w.mainPath)
	if err != nil {
		return fmt.Errorf("failed to create new log file after rotation: %w", err)
	}

	w.file = f
	// Lazy: don't create the new segment's sidecars yet.
	w.idxFile = nil
	w.tidxFile = nil
	w.currentOffset = 0
	w.lineCount = 0
	w.firstWriteMs = 0
	w.lastTidxTime = 0
	w.truncated = true

	w.writeSystemLine(fmt.Sprintf(
		"Log rotated: output exceeded logs.maxSize (%s). Earlier output truncated.",
		config.FormatByteSize(w.maxSize)))

	// Persist rotation metadata so readers can compute virtual offsets
	logutil.WriteLogMeta(w.mainPath, logutil.LogMeta{
		RotatedLines: w.rotatedLines,
		RotatedBytes: w.rotatedBytes,
	})

	return nil
}

func (w *LogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.truncated {
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

	// Persist finalized metadata so readers never need to scan the file
	logutil.WriteLogMeta(w.mainPath, logutil.LogMeta{
		RotatedLines: w.rotatedLines,
		RotatedBytes: w.rotatedBytes,
		FinalLines:   w.lineCount,
		Finalized:    true,
	})

	var errs []error
	if w.file != nil {
		w.file.Sync()
		if err := w.file.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if w.idxFile != nil {
		w.idxFile.Sync()
		if err := w.idxFile.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if w.tidxFile != nil {
		if w.lineCount > 0 && w.lineCount <= math.MaxUint32 {
			now := w.nowMs()
			if now < w.lastTidxTime {
				now = w.lastTidxTime
			}
			var buf [logutil.TimestampIndexEntrySize]byte
			logutil.WriteTimestampEntry(buf[:], logutil.TimestampEntry{
				Line:      uint32(w.lineCount),
				Timestamp: now,
			})
			w.tidxFile.Write(buf[:])
		}
		w.tidxFile.Sync()
		if err := w.tidxFile.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	// Clean up rotation artifacts
	os.Remove(w.mainPath + ".prev")
	os.Remove(w.idxPath + ".prev")
	if w.tidxPath != "" {
		os.Remove(w.tidxPath + ".prev")
	}

	return errors.Join(errs...)
}

// Truncated reports whether any output was discarded.
func (w *LogWriter) Truncated() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.truncated
}

// TotalProduced returns the total bytes the process output before truncation.
func (w *LogWriter) TotalProduced() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.totalProduced
}

// KilledByPolicy reports whether the writer cancelled the run because the
// task's log_on_full = "kill_task" policy tripped. Distinguishes a
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
