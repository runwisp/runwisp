// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runlog

import (
	"bytes"
	"log/slog"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

// captureSlog points the default logger at a buffer at debug level for the
// duration of the test, restoring the previous logger on cleanup.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func sampleRun(reason model.EndReason, exit int) *model.Run {
	start := time.Unix(1700000000, 0)
	end := start.Add(1500 * time.Millisecond)
	return &model.Run{
		ID:        "01HABCRUNID",
		TaskName:  "backup",
		ExitCode:  exit,
		EndReason: model.EndReasonPtr(reason),
		StartAt:   &start,
		EndAt:     &end,
	}
}

func TestSubscribe_LogsRunLifecycle(t *testing.T) {
	bus := events.NewEventBus()
	buf := captureSlog(t)
	defer Subscribe(bus)()

	bus.Publish(events.EventRunStarted, events.RunEvent{Run: sampleRun(model.ReasonSuccess, 0)})
	bus.Publish(events.EventRunCompleted, events.RunEvent{Run: sampleRun(model.ReasonSuccess, 0)})
	bus.Publish(events.EventRunFailed, events.RunEvent{Run: sampleRun(model.ReasonFailed, 2)})

	out := buf.String()
	assert.Contains(t, out, "run started")
	assert.Contains(t, out, "run succeeded")
	assert.Contains(t, out, "run failed")
	assert.Contains(t, out, "task=backup")
	assert.Contains(t, out, "run=01HABCRUNID")
	assert.Contains(t, out, "exit=2")
	assert.Contains(t, out, "reason=failed")
	assert.Contains(t, out, "dur=1.5s")
}

func TestSubscribe_DiskPressure(t *testing.T) {
	bus := events.NewEventBus()
	buf := captureSlog(t)
	defer Subscribe(bus)()

	bus.Publish(events.EventLogDiskPressure, events.LogDiskPressureEvent{
		TaskName: "noisy", RunID: "01HDISK", FreeBytes: 100, MinFreeBytes: 500, KilledTask: true,
	})

	out := buf.String()
	assert.Contains(t, out, "log disk pressure")
	assert.Contains(t, out, "task=noisy")
	assert.Contains(t, out, "killed_task=true")
}

// TestSubscribe_IgnoresNoise locks the deliberate exclusions: per-line log
// events and run churn (created/updated/deleted) must not produce daemon log
// lines — they would bury the signal.
func TestSubscribe_IgnoresNoise(t *testing.T) {
	bus := events.NewEventBus()
	buf := captureSlog(t)
	defer Subscribe(bus)()

	bus.Publish(events.EventLogLine, events.LogLineEvent{TaskName: "backup", Text: "noise"})
	bus.Publish(events.EventRunCreated, events.RunEvent{Run: sampleRun(model.ReasonSuccess, 0)})
	bus.Publish(events.EventRunUpdated, events.RunEvent{Run: sampleRun(model.ReasonSuccess, 0)})

	assert.Empty(t, buf.String(), "log lines and run churn must be ignored")
}

func TestSubscribe_Unsubscribe(t *testing.T) {
	bus := events.NewEventBus()
	buf := captureSlog(t)

	unsub := Subscribe(bus)
	unsub()

	bus.Publish(events.EventRunStarted, events.RunEvent{Run: sampleRun(model.ReasonSuccess, 0)})
	assert.Empty(t, buf.String(), "no lines after unsubscribe")
}

func TestSubscribe_NilRunIgnored(t *testing.T) {
	bus := events.NewEventBus()
	buf := captureSlog(t)
	defer Subscribe(bus)()

	bus.Publish(events.EventRunStarted, events.RunEvent{Run: nil})
	assert.Empty(t, buf.String(), "a malformed event must not panic or log")
}

// captureSlogInfo points the default logger at a buffer at INFO level so DEBUG
// lines are filtered out — used by tests that lock the level-downgrade
// behaviour for expected shutdown events.
func captureSlogInfo(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// TestSubscribe_FailedShutdownReasonsDemoteToDebug locks the contract that
// run-fail events caused by Ctrl+C (ReasonStopped) or the daemon stopping
// (ReasonDaemonStopped) do not produce WARN spam at the default INFO level.
// Real failures still must.
func TestSubscribe_FailedShutdownReasonsDemoteToDebug(t *testing.T) {
	t.Run("ReasonStopped is DEBUG-only", func(t *testing.T) {
		bus := events.NewEventBus()
		buf := captureSlogInfo(t)
		defer Subscribe(bus)()

		bus.Publish(events.EventRunFailed, events.RunEvent{Run: sampleRun(model.ReasonStopped, -1)})
		assert.Empty(t, buf.String(), "operator-initiated stops must not WARN at default verbosity")
	})

	t.Run("ReasonDaemonStopped is DEBUG-only", func(t *testing.T) {
		bus := events.NewEventBus()
		buf := captureSlogInfo(t)
		defer Subscribe(bus)()

		bus.Publish(events.EventRunFailed, events.RunEvent{Run: sampleRun(model.ReasonDaemonStopped, -1)})
		assert.Empty(t, buf.String(), "daemon shutdown terminations must not WARN at default verbosity")
	})

	t.Run("ReasonFailed still WARNs", func(t *testing.T) {
		bus := events.NewEventBus()
		buf := captureSlogInfo(t)
		defer Subscribe(bus)()

		bus.Publish(events.EventRunFailed, events.RunEvent{Run: sampleRun(model.ReasonFailed, 2)})
		out := buf.String()
		assert.Contains(t, out, "run failed")
		assert.Contains(t, out, "level=WARN", "genuine failures must stay loud — the whole point of the demotion is not to hide them")
	})
}
