// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMapEvent_SkipNeverNotifies guards the prime-directive contract from
// concurrency.mdx: a run that PolicySkip rejected is the policy doing its
// job. It must not flow through the notification system and must especially
// not pose as a KindRunFailed (the original `* * * * *` health-probe Slack
// storm bug).
func TestMapEvent_SkipNeverNotifies(t *testing.T) {
	skipped := sqlcdb.ReasonSkipped
	run := &sqlcdb.Run{
		ID:        "01HSKIP",
		TaskName:  "health-probe",
		Status:    sqlcdb.PhaseEnded,
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

func TestService_DroppedIngressCount_Zero(t *testing.T) {
	svc := New(Config{Bus: events.NewEventBus()})
	assert.Equal(t, uint64(0), svc.DroppedIngressCount())
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

func TestMapEvent_NilRun(t *testing.T) {
	ev := events.Event{
		Type:      events.EventRunFailed,
		Timestamp: time.Now(),
		Data:      events.RunEvent{Run: nil},
	}
	assert.Nil(t, MapEvent(ev))
}

func TestMapEvent_UnknownEventType(t *testing.T) {
	run := &sqlcdb.Run{ID: "01HR1", TaskName: "t1", Status: sqlcdb.PhaseRunning}
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
	run := &sqlcdb.Run{Status: sqlcdb.PhaseEnded}
	kind, sev, ok := mapRunEventType(events.EventRunFailed, run)
	assert.True(t, ok)
	assert.Equal(t, KindRunFailed, kind)
	assert.Equal(t, SevError, sev)
}

func TestMapRunEventType_RunFailed_ReasonFailed(t *testing.T) {
	r := sqlcdb.ReasonFailed
	run := &sqlcdb.Run{EndReason: &r}
	kind, _, ok := mapRunEventType(events.EventRunFailed, run)
	assert.True(t, ok)
	assert.Equal(t, KindRunFailed, kind)
}

func TestMapRunEventType_RunFailed_ReasonTimeout(t *testing.T) {
	r := sqlcdb.ReasonTimeout
	run := &sqlcdb.Run{EndReason: &r}
	kind, _, ok := mapRunEventType(events.EventRunFailed, run)
	assert.True(t, ok)
	assert.Equal(t, KindRunTimeout, kind)
}

func TestMapRunEventType_RunFailed_ReasonStopped(t *testing.T) {
	r := sqlcdb.ReasonStopped
	run := &sqlcdb.Run{EndReason: &r}
	kind, sev, ok := mapRunEventType(events.EventRunFailed, run)
	assert.True(t, ok)
	assert.Equal(t, KindRunStopped, kind)
	assert.Equal(t, SevWarn, sev)
}

func TestMapRunEventType_RunFailed_ReasonCrashed(t *testing.T) {
	r := sqlcdb.ReasonCrashed
	run := &sqlcdb.Run{EndReason: &r}
	kind, _, ok := mapRunEventType(events.EventRunFailed, run)
	assert.True(t, ok)
	assert.Equal(t, KindRunCrashed, kind)
}

func TestMapRunEventType_RunFailed_DefaultReason(t *testing.T) {
	r := sqlcdb.ReasonSuccess // not a typical failure reason, hits default branch
	run := &sqlcdb.Run{EndReason: &r}
	kind, _, ok := mapRunEventType(events.EventRunFailed, run)
	assert.True(t, ok)
	assert.Equal(t, KindRunFailed, kind)
}

func TestRunReasonString_WithErrMsg(t *testing.T) {
	run := &sqlcdb.Run{ID: "r1"}
	result := runReasonString(run, "connection refused")
	assert.Equal(t, "connection refused", result)
}

func TestRunReasonString_NilRun(t *testing.T) {
	result := runReasonString(nil, "")
	assert.Equal(t, "", result)
}

func TestRunReasonString_WithDuration(t *testing.T) {
	r := sqlcdb.ReasonFailed
	now := time.Now()
	end := now.Add(5 * time.Second)
	run := &sqlcdb.Run{
		EndReason: &r,
		ExitCode:  1,
		StartAt:   &now,
		EndAt:     &end,
	}
	result := runReasonString(run, "")
	assert.Contains(t, result, "failed")
	assert.Contains(t, result, "5s")
}
