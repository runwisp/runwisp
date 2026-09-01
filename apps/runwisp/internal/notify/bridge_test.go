// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package notify

import (
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMapEvent_SkipNeverNotifies guards the prime-directive contract from
// concurrency.mdx: a run that PolicySkip rejected is the policy doing its
// job. It must not flow through the notification system and must especially
// not pose as a KindRunFailed (the original `* * * * *` health-probe Slack
// storm bug).
func TestMapEvent_SkipNeverNotifies(t *testing.T) {
	skipped := model.ReasonSkipped
	run := &model.Run{
		ID:        "01HSKIP",
		TaskName:  "health-probe",
		Status:    model.PhaseEnded,
		EndReason: &skipped,
		ExitCode:  -1,
	}

	for _, et := range []events.EventType{
		events.EventRunFailed,
		events.EventRunCompleted,
		events.EventRunStarted,
	} {
		ev := events.Event{
			Type:      et,
			Timestamp: time.Now(),
			Data:      events.RunEvent{Run: run},
		}
		got := MapEvent(ev)
		// EventRunStarted maps to KindRunStarted regardless of EndReason —
		// that's correct (a started event is about the lifecycle, not the
		// outcome). The bug we're guarding against is EventRunFailed for a
		// skipped run mapping to KindRunFailed/Stopped/etc.
		if et == events.EventRunFailed {
			assert.Nil(t, got, "skipped runs must not map to any notification on EventRunFailed")
		}
		_ = got
	}
}

// TestMapEvent_LogDiskPressure verifies the new event surface introduced
// when min_free_space stops honouring per-task log_on_full silently.
func TestMapEvent_LogDiskPressure(t *testing.T) {
	ev := events.Event{
		Type:      events.EventLogDiskPressure,
		Timestamp: time.Now(),
		Data: events.LogDiskPressureEvent{
			TaskName:     "etl",
			RunID:        "01HRUN",
			FreeBytes:    100,
			MinFreeBytes: 1024,
			KilledTask:   true,
		},
	}
	got := MapEvent(ev)
	require.NotNil(t, got)
	assert.Equal(t, KindLogDiskPressure, got.Kind)
	assert.Equal(t, SevWarn, got.Severity)
	assert.Equal(t, "etl", got.TaskName)
	assert.Equal(t, true, got.Extra["killed_task"])
	assert.Equal(t, int64(100), got.Extra["free_bytes"])
	assert.Equal(t, int64(1024), got.Extra["min_free_bytes"])
}

// TestMapEvent_ServiceFatal verifies the give-up signal maps to the dedicated
// service.fatal kind at error severity, carrying the instance context so the
// notification names which instance died and why.
func TestMapEvent_ServiceFatal(t *testing.T) {
	ev := events.Event{
		Type:      events.EventServiceFatal,
		Timestamp: time.Now(),
		Data: events.ServiceFatalEvent{
			TaskName:      "worker",
			InstanceIndex: 1,
			Attempts:      4,
			LastExitCode:  7,
		},
	}
	got := MapEvent(ev)
	require.NotNil(t, got)
	assert.Equal(t, KindServiceFatal, got.Kind)
	assert.Equal(t, SevError, got.Severity)
	assert.Equal(t, "worker", got.TaskName)
	assert.Equal(t, 1, got.Extra["instance_index"])
	assert.Equal(t, 4, got.Extra["attempts"])
	assert.Equal(t, 7, got.Extra["last_exit_code"])
}

// TestMapRunEventType_StartFailedSuppressed guards the no-double-bell contract:
// the start_failed run row is suppressed from notify because the dedicated
// service.fatal event already rings the bell for the give-up.
func TestMapRunEventType_StartFailedSuppressed(t *testing.T) {
	r := model.ReasonStartFailed
	run := &model.Run{EndReason: &r}
	_, _, ok := mapRunEventType(events.EventRunFailed, run)
	assert.False(t, ok, "start_failed run row must not produce a second notification")
}

func TestMapEvent_NilRun(t *testing.T) {
	ev := events.Event{
		Type:      events.EventRunFailed,
		Timestamp: time.Now(),
		Data:      events.RunEvent{Run: nil},
	}
	assert.Nil(t, MapEvent(ev))
}

func TestMapEvent_UnknownEventType(t *testing.T) {
	run := &model.Run{ID: "01HR1", TaskName: "t1", Status: model.PhaseRunning}
	ev := events.Event{
		Type: events.EventRunCreated,
		Data: events.RunEvent{Run: run},
	}
	assert.Nil(t, MapEvent(ev))
}

func TestDiskPressureReason_NotKilled(t *testing.T) {
	d := events.LogDiskPressureEvent{FreeBytes: 50, MinFreeBytes: 1000, KilledTask: false}
	reason := diskPressureReason(d)
	assert.Contains(t, reason, "paused")
	assert.NotContains(t, reason, "killed")
}

func TestMapRunEventType_RunFailed_NilEndReason(t *testing.T) {
	run := &model.Run{Status: model.PhaseEnded}
	kind, sev, ok := mapRunEventType(events.EventRunFailed, run)
	assert.True(t, ok)
	assert.Equal(t, KindRunFailed, kind)
	assert.Equal(t, SevError, sev)
}

func TestMapRunEventType_RunFailed_ReasonFailed(t *testing.T) {
	r := model.ReasonFailed
	run := &model.Run{EndReason: &r}
	kind, _, ok := mapRunEventType(events.EventRunFailed, run)
	assert.True(t, ok)
	assert.Equal(t, KindRunFailed, kind)
}

func TestMapRunEventType_RunFailed_ReasonTimeout(t *testing.T) {
	r := model.ReasonTimeout
	run := &model.Run{EndReason: &r}
	kind, _, ok := mapRunEventType(events.EventRunFailed, run)
	assert.True(t, ok)
	assert.Equal(t, KindRunTimeout, kind)
}

func TestMapRunEventType_RunFailed_ReasonStopped(t *testing.T) {
	r := model.ReasonStopped
	run := &model.Run{EndReason: &r}
	kind, sev, ok := mapRunEventType(events.EventRunFailed, run)
	assert.True(t, ok)
	assert.Equal(t, KindRunStopped, kind)
	assert.Equal(t, SevWarn, sev)
}

func TestMapRunEventType_RunFailed_ReasonCrashed(t *testing.T) {
	r := model.ReasonCrashed
	run := &model.Run{EndReason: &r}
	kind, _, ok := mapRunEventType(events.EventRunFailed, run)
	assert.True(t, ok)
	assert.Equal(t, KindRunCrashed, kind)
}

func TestMapRunEventType_RunFailed_ReasonMissed(t *testing.T) {
	r := model.ReasonMissed
	run := &model.Run{EndReason: &r}
	kind, sev, ok := mapRunEventType(events.EventRunFailed, run)
	assert.True(t, ok)
	assert.Equal(t, KindRunMissed, kind)
	assert.Equal(t, SevError, sev, "a missed run alerts at failure level")
}

// TestMapRunEventType_RunFailed_ReasonDaemonStopped guards against a routine
// daemon restart/upgrade posing as a task failure: manager.go explicitly
// records "a daemon-stopped exit is not a failure", and the cloud tracker
// maps it to ExecutionStatusStopped alongside ReasonStopped — the bridge must
// agree instead of falling through to its default KindRunFailed/SevError.
func TestMapRunEventType_RunFailed_ReasonDaemonStopped(t *testing.T) {
	r := model.ReasonDaemonStopped
	run := &model.Run{EndReason: &r}
	kind, sev, ok := mapRunEventType(events.EventRunFailed, run)
	assert.True(t, ok)
	assert.Equal(t, KindRunStopped, kind)
	assert.Equal(t, SevWarn, sev)
}

// TestMapRunEventType_RunFailed_ReasonDSTSkipped guards against the annual
// DST fall-back dedup posing as a task failure. It's produced by the same
// RecordSkippedFiring path as ReasonSkipped (the policy doing its job, never
// routed through notifications) and the cloud tracker maps it to
// ExecutionStatusSkipped, not Failed — the bridge must mute it the same way
// it mutes ReasonSkipped instead of falling through to its default.
func TestMapRunEventType_RunFailed_ReasonDSTSkipped(t *testing.T) {
	r := model.ReasonDSTSkipped
	run := &model.Run{EndReason: &r}
	_, _, ok := mapRunEventType(events.EventRunFailed, run)
	assert.False(t, ok, "a DST dedup is not a failure and must not notify")
}

func TestMapRunEventType_RunFailed_DefaultReason(t *testing.T) {
	r := model.ReasonSuccess // not a typical failure reason, hits default branch
	run := &model.Run{EndReason: &r}
	kind, _, ok := mapRunEventType(events.EventRunFailed, run)
	assert.True(t, ok)
	assert.Equal(t, KindRunFailed, kind)
}

func TestRunReasonString_WithErrMsg(t *testing.T) {
	run := &model.Run{ID: "r1"}
	result := runReasonString(run, "connection refused")
	assert.Equal(t, "connection refused", result)
}

func TestRunReasonString_NilRun(t *testing.T) {
	result := runReasonString(nil, "")
	assert.Equal(t, "", result)
}

func TestRunReasonString_WithDuration(t *testing.T) {
	r := model.ReasonFailed
	now := time.Now()
	end := now.Add(5 * time.Second)
	run := &model.Run{
		EndReason: &r,
		ExitCode:  1,
		StartedAt: &now,
		EndedAt:   &end,
	}
	result := runReasonString(run, "")
	assert.Contains(t, result, "failed")
	assert.Contains(t, result, "5s")
}
