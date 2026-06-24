// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package logutil

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/internal/model"
)

// LogIndexInterval is the number of lines between index entries in log files.
// Used by both the executor (writer) and server (reader) packages.
const LogIndexInterval = 1024

// ResolveRunLogPath computes the log path for a run from its fields.
// Format: {logDir}/{sanitizedTask}/{YYYYMMDD}_{HHMMSS}_{ulidSuffix}.log
// Timestamps are formatted in UTC so the on-disk cadence stays stable when
// the host timezone changes (DST flips, traveling laptops, container moves).
func ResolveRunLogPath(logDir, taskName, runID string, createdAt time.Time) string {
	sanitized := model.SanitizeTaskName(taskName)
	ts := createdAt.UTC().Format("20060102_150405")
	suffix := runID[len(runID)-4:]
	return filepath.Join(logDir, sanitized, fmt.Sprintf("%s_%s.log", ts, suffix))
}

// PrevPath returns the rotated-away segment path for a log file. Like the
// sidecar container it is hidden (leading dot) so it never clutters an `ls`
// while a tail-mode rotation keeps the previous segment around.
func PrevPath(logPath string) string {
	return filepath.Join(filepath.Dir(logPath), "."+filepath.Base(logPath)+".prev")
}

// RemoveLogFiles removes a log file and its associated sidecar/rotation
// artifacts: the log itself, the consolidated container, and any rotated-away
// segment.
func RemoveLogFiles(logPath string) {
	if logPath == "" {
		return
	}
	for _, p := range []string{logPath, MetaPath(logPath), PrevPath(logPath)} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			slog.Warn("Failed to delete log file", "path", p, "err", err)
		}
	}
}

// RemoveEmptyParents removes empty directories between path's parent and
// stopAt (exclusive). Stops at the first non-empty or non-removable directory.
func RemoveEmptyParents(path, stopAt string) {
	dir := filepath.Dir(path)
	absStop, _ := filepath.Abs(stopAt)
	for {
		absDir, _ := filepath.Abs(dir)
		if absDir == absStop || dir == "." || dir == "/" {
			break
		}
		if err := os.Remove(dir); err != nil {
			break
		}
		dir = filepath.Dir(dir)
	}
}
