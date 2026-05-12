// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"fmt"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
)

// MapEvent converts an internal events.Event into a notify.Event. Returns nil
// for events the notification subsystem ignores (log lines, run.created /
// run.updated). Currently handles RunEvent and LogDiskPressureEvent payloads.
func MapEvent(e events.Event) *Event {
	switch d := e.Data.(type) {
	case events.RunEvent:
		if d.Run == nil {
			return nil
		}
		kind, severity, ok := mapRunEventType(e.Type, d.Run)
		if !ok {
			return nil
		}
		return &Event{
			Kind:      kind,
			Severity:  severity,
			Timestamp: e.Timestamp,
			TaskName:  d.Run.TaskName,
			Run:       d.Run,
			Reason:    runReasonString(d.Run, d.Error),
		}
	case events.LogDiskPressureEvent:
		return &Event{
			Kind:      KindLogDiskPressure,
			Severity:  SevWarn,
			Timestamp: e.Timestamp,
			TaskName:  d.TaskName,
			Reason:    diskPressureReason(d),
			Extra: map[string]any{
				"run_id":         d.RunID,
				"free_bytes":     d.FreeBytes,
				"min_free_bytes": d.MinFreeBytes,
				"killed_task":    d.KilledTask,
			},
		}
	default:
		return nil
	}
}

func diskPressureReason(d events.LogDiskPressureEvent) string {
	if d.KilledTask {
		return fmt.Sprintf("disk space below %d bytes (free %d); task killed by log_on_full=\"kill_task\"",
			d.MinFreeBytes, d.FreeBytes)
	}
	return fmt.Sprintf("disk space below %d bytes (free %d); log output paused",
		d.MinFreeBytes, d.FreeBytes)
}

// mapRunEventType collapses (events.EventType, run state) into the public Kind
// + Severity. Returns ok=false for events the notify subsystem ignores
// (e.g. EventLogLine).
func mapRunEventType(t events.EventType, run *model.Run) (Kind, Severity, bool) {
	switch t {
	case events.EventRunStarted:
		return KindRunStarted, SevInfo, true
	case events.EventRunCompleted:
		return KindRunSucceeded, SevInfo, true
	case events.EventRunFailed:
		if run.EndReason == nil {
			return KindRunFailed, SevError, true
		}
		switch *run.EndReason {
		case model.ReasonFailed, model.ReasonLogOverflow:
			return KindRunFailed, SevError, true
		case model.ReasonTimeout:
			return KindRunTimeout, SevError, true
		case model.ReasonStopped:
			return KindRunStopped, SevWarn, true
		case model.ReasonCrashed:
			return KindRunCrashed, SevError, true
		case model.ReasonSkipped:
			// PolicySkip is the policy doing its job, not a failure: never
			// route it through the notification system. Operators who care
			// about chronic skips read the run history.
			return "", "", false
		default:
			return KindRunFailed, SevError, true
		}
	default:
		// EventRunCreated, EventRunUpdated, EventLogLine: not surfaced as
		// notifications. Created is implicit in started/completed; updated
		// reflects intermediate state changes that would just be noise.
		return "", "", false
	}
}

func runReasonString(run *model.Run, errMsg string) string {
	if errMsg != "" {
		return errMsg
	}
	if run == nil {
		return ""
	}
	if run.EndReason != nil && *run.EndReason != model.ReasonSuccess {
		duration := ""
		if run.StartAt != nil && run.EndAt != nil {
			duration = fmt.Sprintf(" after %s", run.EndAt.Sub(*run.StartAt).Round(time.Second))
		}
		if run.ExitCode != 0 {
			return fmt.Sprintf("%s exit %d%s", *run.EndReason, run.ExitCode, duration)
		}
		return fmt.Sprintf("%s%s", *run.EndReason, duration)
	}
	return ""
}
