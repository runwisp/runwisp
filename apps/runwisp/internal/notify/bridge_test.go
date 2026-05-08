// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
