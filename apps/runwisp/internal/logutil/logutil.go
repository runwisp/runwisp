// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package logutil

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/charmbracelet/log"
	"github.com/runwisp/runwisp/internal/model"
)

// LogIndexInterval is the number of lines between index entries in log files.
// Used by both the executor (writer) and server (reader) packages.
const LogIndexInterval = 1024

// RunLogPath generates a human-readable, filesystem-friendly log path.
// Format: {logDir}/{sanitizedTask}/{YYYYMMDD}_{HHMMSS}_{ulidSuffix}.log
func RunLogPath(logDir, sanitizedTaskName, runID string, createdAt time.Time) string {
	ts := createdAt.Format("20060102_150405")
	suffix := runID[len(runID)-4:]
	return filepath.Join(logDir, sanitizedTaskName, fmt.Sprintf("%s_%s.log", ts, suffix))
}

// TaskLogDir returns the subdirectory for a task's logs.
func TaskLogDir(logDir, sanitizedTaskName string) string {
	return filepath.Join(logDir, sanitizedTaskName)
}

// ResolveRunLogPath computes the log path for a run from its fields.
func ResolveRunLogPath(logDir, taskName, runID string, createdAt time.Time) string {
	return RunLogPath(logDir, model.SanitizeTaskName(taskName), runID, createdAt)
}

// RemoveLogFiles removes a log file and its associated index/rotation artifacts.
func RemoveLogFiles(logPath string) {
	if logPath == "" {
		return
	}
	for _, suffix := range []string{"", ".idx", ".prev", ".idx.prev", ".tidx", ".tidx.prev", ".meta"} {
		p := logPath + suffix
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			log.Warn("Failed to delete log file", "path", p, "err", err)
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
