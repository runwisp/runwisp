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
	// diskCheckInterval is how many bytes between disk-space probes.
	diskCheckInterval int64 = 10 * 1024 * 1024 // 10 MB
)

// LogWriter writes log output to a file with an accompanying binary index,
// enforcing optional per-run size limits with configurable overflow behaviour.
type LogWriter struct {
	mu       sync.Mutex
	mainPath string
	idxPath  string
	tidxPath string

	file     *os.File
	idxFile  *os.File
	tidxFile *os.File

	// Size limiting
	maxSize    int64  // 0 = unlimited
	overflow   string // model.LogOverflow* constants
	cancelFunc func() // cancel context for kill_task mode

	// Disk space monitoring
	minFreeDisk   int64  // 0 = disabled
	logDir        string // for statfs calls
	lastDiskCheck int64  // offset at last disk check

	// State
	currentOffset int64
	lineCount     int64
	totalProduced int64 // total bytes the process output (before any truncation)
	truncated     bool
	stopped       bool // drop_new mode or disk-full: no more writes accepted

	// Rotation bookkeeping: cumulative counts from rotated-away files
	rotatedLines int64
	rotatedBytes int64

	// Timestamp index
	lastTidxTime int64        // unix ms of last tidx entry
	nowMs        func() int64 // clock for timestamp index; defaults to time.Now().UnixMilli
}

// LogWriterOpts configures a LogWriter.
type LogWriterOpts struct {
	LogPath     string
	IdxPath     string
	TidxPath    string // timestamp index path (.tidx)
	MaxSize     int64  // 0 = unlimited
	Overflow    string // model.LogOverflow* constants; defaults to drop_old
	CancelFunc  func() // for kill_task mode
	MinFreeDisk int64  // 0 = disabled
	LogDir      string
}

func NewLogWriter(opts LogWriterOpts) (*LogWriter, error) {
	f, err := os.Create(opts.LogPath)
	if err != nil {
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}

	idx, err := os.Create(opts.IdxPath)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("failed to create log index file: %w", err)
	}

	var tidx *os.File
	if opts.TidxPath != "" {
		tidx, err = os.Create(opts.TidxPath)
		if err != nil {
			f.Close()
			idx.Close()
			return nil, fmt.Errorf("failed to create timestamp index file: %w", err)
		}
	}

	overflow := opts.Overflow
	if overflow == "" {
		overflow = model.LogOverflowDropOld
	}

	return &LogWriter{
		mainPath:    opts.LogPath,
		idxPath:     opts.IdxPath,
		tidxPath:    opts.TidxPath,
		file:        f,
		idxFile:     idx,
		tidxFile:    tidx,
		maxSize:     opts.MaxSize,
		overflow:    overflow,
		cancelFunc:  opts.CancelFunc,
		minFreeDisk: opts.MinFreeDisk,
		logDir:      opts.LogDir,
		nowMs:       func() int64 { return time.Now().UnixMilli() },
	}, nil
}

func (w *LogWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.totalProduced += int64(len(p))

	if w.stopped {
		return len(p), nil
	}

	// Periodic disk-space check
	if w.minFreeDisk > 0 && w.currentOffset-w.lastDiskCheck > diskCheckInterval {
		w.lastDiskCheck = w.currentOffset
		if free := freeDiskSpace(w.logDir); free >= 0 && free < w.minFreeDisk {
			w.writeSystemLine(fmt.Sprintf(
				"Log output stopped: disk space critically low (%s free, minimum %s required).",
				config.FormatByteSize(free), config.FormatByteSize(w.minFreeDisk)))
			w.stopped = true
			w.truncated = true
			return len(p), nil
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
			return len(p), nil

		case model.LogOverflowKillTask:
			w.writeSystemLine(fmt.Sprintf(
				"Process killed: log output exceeded log_max_size (%s).",
				config.FormatByteSize(w.maxSize)))
			w.truncated = true
			w.stopped = true
			if w.cancelFunc != nil {
				w.cancelFunc()
			}
			return len(p), nil

		case model.LogOverflowDropOld:
			if err := w.rotateTail(); err != nil {
				slog.Error("Failed to rotate log file, continuing without rotation", "err", err)
			}
		}
	}

	n, err = w.file.Write(p)
	if err != nil {
		return n, err
	}

	if n > 0 {
		if w.lineCount%logutil.LogIndexInterval == 0 {
			var buf [8]byte
			binary.LittleEndian.PutUint64(buf[:], uint64(w.currentOffset))
			if _, idxErr := w.idxFile.Write(buf[:]); idxErr != nil {
				slog.Warn("Failed to write to log index", "err", idxErr)
			}
		}
		w.writeTidxEntry()
		w.currentOffset += int64(n)
		w.lineCount++
	}

	return n, nil
}

func (w *LogWriter) writeSystemLine(msg string) {
	line := fmt.Sprintf("[%s] [SYSTEM] %s\n",
		time.Now().Format("2006-01-02 15:04:05"), msg)
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
// Caller must hold mu.
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
	if err := w.idxFile.Sync(); err != nil {
		slog.Warn("Failed to sync log index before rotation", "err", err)
	}
	if err := w.idxFile.Close(); err != nil {
		slog.Warn("Failed to close log index before rotation", "err", err)
	}
	if w.tidxFile != nil {
		w.tidxFile.Sync()
		w.tidxFile.Close()
	}

	prevPath := w.mainPath + ".prev"
	prevIdxPath := w.idxPath + ".prev"
	prevTidxPath := w.tidxPath + ".prev"

	// Delete previous rotation artifact
	os.Remove(prevPath)
	os.Remove(prevIdxPath)
	os.Remove(prevTidxPath)

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
		w.idxFile, reopenErr = os.OpenFile(w.idxPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if reopenErr != nil {
			w.stopped = true
			slog.Error("Failed to re-open index file after rotation failure", "err", reopenErr)
		}
		return fmt.Errorf("failed to rotate log: %w", err)
	}
	os.Rename(w.idxPath, prevIdxPath) // best-effort; stale idx is harmless
	if w.tidxPath != "" {
		os.Rename(w.tidxPath, prevTidxPath)
	}

	f, err := os.Create(w.mainPath)
	if err != nil {
		return fmt.Errorf("failed to create new log file after rotation: %w", err)
	}
	idx, err := os.Create(w.idxPath)
	if err != nil {
		f.Close()
		return fmt.Errorf("failed to create new index file after rotation: %w", err)
	}

	w.file = f
	w.idxFile = idx
	w.currentOffset = 0
	w.lineCount = 0
	w.truncated = true

	// Recreate timestamp index
	if w.tidxPath != "" {
		tidx, tidxErr := os.Create(w.tidxPath)
		if tidxErr != nil {
			slog.Warn("Failed to create timestamp index after rotation", "err", tidxErr)
			w.tidxFile = nil
		} else {
			w.tidxFile = tidx
		}
	}

	// Write initial index entry (offset 0) so the new file has an index
	// even before LogIndexInterval lines are written.
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], 0)
	if _, err := w.idxFile.Write(buf[:]); err != nil {
		slog.Warn("Failed to write initial index entry after rotation", "err", err)
	}

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

// freeDiskSpace returns available bytes on the filesystem containing path,
// or -1 if the check fails.
func freeDiskSpace(path string) int64 {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return -1
	}
	return int64(stat.Bavail) * int64(stat.Bsize)
}
