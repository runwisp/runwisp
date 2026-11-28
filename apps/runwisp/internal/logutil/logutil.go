// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package logutil

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

// LogIndexInterval is the number of lines between index entries in log files.
// Used by both the executor (writer) and server (reader) packages.
const LogIndexInterval = 1024

// ResolveRunLogPath computes the log path for a run from its fields.
// Format: {logDir}/{sanitizedTask}/{YYYYMMDD}_{HHMMSS}_{runID}.log
// Timestamps are formatted in UTC so the on-disk cadence stays stable when
// the host timezone changes (DST flips, traveling laptops, container moves).
// The full run ID is used (not a truncated suffix) so two runs of the same
// task landing in the same wall-clock second — e.g. concurrent `instances`,
// or an unrelated retrigger a moment after a scheduled fire — can never
// collide onto the same path: nothing parses this filename back into an ID,
// so there is no readability trade-off to make here.
func ResolveRunLogPath(logDir, taskName, runID string, createdAt time.Time) string {
	sanitized := model.SanitizeTaskName(taskName)
	ts := createdAt.UTC().Format("20060102_150405")
	return filepath.Join(logDir, sanitized, fmt.Sprintf("%s_%s.log", ts, runID))
}

// PrevPath returns the rotated-away segment path for a log file. Like the
// sidecar container it is hidden (leading dot) so it never clutters an `ls`
// while a tail-mode rotation keeps the previous segment around.
func PrevPath(logPath string) string {
	return filepath.Join(filepath.Dir(logPath), "."+filepath.Base(logPath)+".prev")
}

// LogFiles lists every file a run's log owns: the rotated-away segment, the
// log itself, then the sidecar. RemoveLogFiles deletes them in this order so
// the sidecar stays available while any log segment remains.
func LogFiles(logPath string) []string {
	return []string{PrevPath(logPath), logPath, MetaPath(logPath)}
}

// RemoveLogFiles removes a log file and its associated sidecar/rotation
// artifacts: the log itself, the consolidated container, and any rotated-away
// segment.
func RemoveLogFiles(logPath string) error {
	if logPath == "" {
		return nil
	}
	for _, p := range LogFiles(logPath) {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// RemoveRunLogs removes a run's log files, then any task directories left
// empty under logDir.
func RemoveRunLogs(logDir, taskName, runID string, createdAt time.Time) error {
	if logDir == "" {
		return nil
	}
	logPath := ResolveRunLogPath(logDir, taskName, runID, createdAt)
	if err := RemoveLogFiles(logPath); err != nil {
		return err
	}
	RemoveEmptyParents(logPath, logDir)
	return nil
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
