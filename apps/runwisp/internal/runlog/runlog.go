// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package runlog emits one concise slog line per run lifecycle transition for
// the headless daemon. Without it a `docker logs` / journald operator sees a
// black box once the startup banner scrolls past — directly undercutting Prime
// Directive 1 ("Nothing silently fails") on the one surface with no TUI/UI in
// front of the user. The interactive TUI already visualizes runs, so this is
// wired only on the daemon boot path.
package runlog

import (
	"context"
	"log/slog"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
)

// Subscribe wires the run-lifecycle logger onto bus and returns an unsubscribe
// func to call at shutdown. It listens only to start/complete/fail and log disk
// pressure — never EventLogLine (per-output-line, far too noisy) and not the
// created/updated/deleted churn events, which carry no operator-facing signal.
func Subscribe(bus *events.Bus) func() {
	unsubs := []func(){
		bus.Subscribe(events.EventRunStarted, logStarted),
		bus.Subscribe(events.EventRunCompleted, logCompleted),
		bus.Subscribe(events.EventRunFailed, logFailed),
		bus.Subscribe(events.EventLogDiskPressure, logDiskPressure),
	}
	return func() {
		for _, unsub := range unsubs {
			unsub()
		}
	}
}

func logStarted(e events.Event) {
	run, ok := runFrom(e)
	if !ok {
		return
	}
	slog.Info("run started", "task", run.TaskName, "run", run.ID)
}

func logCompleted(e events.Event) {
	run, ok := runFrom(e)
	if !ok {
		return
	}
	slog.Info("run succeeded",
		"task", run.TaskName, "run", run.ID,
		"exit", run.ExitCode, "dur", runDuration(run))
}

func logFailed(e events.Event) {
	run, ok := runFrom(e)
	if !ok {
		return
	}
	// Shutdown-triggered terminations (operator Ctrl+C, daemon stop) fire one
	// "run failed" event per in-flight run and would otherwise spam WARN
	// during the very narrative the "received signal, shutting down" line
	// already framed. Downgrade to DEBUG; genuine failures stay at WARN.
	level := slog.LevelWarn
	if run.EndReason != nil {
		switch *run.EndReason {
		case model.ReasonStopped, model.ReasonDaemonStopped:
			level = slog.LevelDebug
		}
	}
	slog.Log(context.Background(), level, "run failed",
		"task", run.TaskName, "run", run.ID,
		"exit", run.ExitCode, "reason", reasonString(run),
		"dur", runDuration(run))
}

func logDiskPressure(e events.Event) {
	dp, ok := e.Data.(events.LogDiskPressureEvent)
	if !ok {
		return
	}
	slog.Warn("log disk pressure",
		"task", dp.TaskName, "run", dp.RunID,
		"free_bytes", dp.FreeBytes, "min_free_bytes", dp.MinFreeBytes,
		"killed_task", dp.KilledTask)
}

func runFrom(e events.Event) (*model.Run, bool) {
	re, ok := e.Data.(events.RunEvent)
	if !ok || re.Run == nil {
		return nil, false
	}
	return re.Run, true
}

func reasonString(run *model.Run) string {
	if run.EndReason == nil {
		return string(model.ReasonFailed)
	}
	return string(*run.EndReason)
}

// runDuration returns the wall-clock run duration rounded to a readable
// precision, or 0 when the run never started (e.g. skipped by a concurrency
// policy) so the line still parses cleanly.
func runDuration(run *model.Run) time.Duration {
	if run.StartAt == nil || run.EndAt == nil {
		return 0
	}
	return run.EndAt.Sub(*run.StartAt).Round(time.Millisecond)
}
