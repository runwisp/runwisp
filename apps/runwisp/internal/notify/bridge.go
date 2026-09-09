// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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
		kind, ok := mapRunEventType(e.Type, d.Run)
		if !ok {
			return nil
		}
		return &Event{
			Kind:      kind,
			Severity:  runSeverity(kind, d.Run.IsFailure),
			IsFailure: d.Run.IsFailure,
			Timestamp: e.Timestamp,
			TaskName:  d.Run.TaskName,
			Run:       d.Run,
			LogPath:   d.LogPath,
			Reason:    runReasonString(d.Run, d.Error),
		}
	case events.LogDiskPressureEvent:
		return &Event{
			Kind:      KindLogDiskPressure,
			Severity:  SevWarn,
			IsFailure: false,
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
	case events.ServiceFatalEvent:
		return &Event{
			Kind:      KindServiceFatal,
			Severity:  SevError,
			IsFailure: true,
			Timestamp: e.Timestamp,
			TaskName:  d.TaskName,
			Reason:    serviceFatalReason(d),
			Extra: map[string]any{
				"instance_index": d.InstanceIndex,
				"attempts":       d.Attempts,
				"last_exit_code": d.LastExitCode,
			},
		}
	default:
		return nil
	}
}

func serviceFatalReason(d events.ServiceFatalEvent) string {
	return fmt.Sprintf("instance %d failed to start %d times in a row (last exit %d); supervisor gave up",
		d.InstanceIndex, d.Attempts, d.LastExitCode)
}

func diskPressureReason(d events.LogDiskPressureEvent) string {
	if d.KilledTask {
		return fmt.Sprintf("disk space below %d bytes (free %d); task killed by log_on_full=\"kill\"",
			d.MinFreeBytes, d.FreeBytes)
	}
	return fmt.Sprintf("disk space below %d bytes (free %d); log output paused",
		d.MinFreeBytes, d.FreeBytes)
}

// runSeverity derives an event's severity from its classified failure bit: a
// failure is an error, a start/success is informational, and anything else
// (a non-failure terminal reason like a demoted `missed` or an ordinary
// `stopped`) is a warning. Kept separate from the failure routing decision,
// which reads IsFailure directly.
func runSeverity(kind Kind, isFailure bool) Severity {
	if isFailure {
		return SevError
	}
	switch kind {
	case KindRunStarted, KindRunSucceeded:
		return SevInfo
	default:
		return SevWarn
	}
}

// mapRunEventType collapses (events.EventType, run state) into the public Kind
// used for message text and icon. Returns ok=false for events the notify
// subsystem ignores (e.g. EventLogLine). Whether the event *pages* and its
// severity are decided from run.IsFailure, not from the Kind returned here.
func mapRunEventType(t events.EventType, run *model.Run) (Kind, bool) {
	switch t {
	case events.EventRunStarted:
		return KindRunStarted, true
	case events.EventRunCompleted:
		return KindRunSucceeded, true
	case events.EventRunFailed:
		if run.EndReason == nil {
			return KindRunFailed, true
		}
		switch *run.EndReason {
		case model.ReasonFailed, model.ReasonLogOverflow:
			return KindRunFailed, true
		case model.ReasonTimeout:
			return KindRunTimeout, true
		case model.ReasonStopped, model.ReasonDaemonStopped:
			return KindRunStopped, true
		case model.ReasonCrashed:
			return KindRunCrashed, true
		case model.ReasonMissed:
			// A scheduled run that never happened. Whether it pages is decided by
			// the task's `failures` policy (run.IsFailure); the browsable run row
			// is always persisted regardless, upstream of notify.
			return KindRunMissed, true
		case model.ReasonSkipped, model.ReasonDSTSkipped:
			// PolicySkip and the DST fall-back dedup are the scheduler doing
			// its job, not a failure: never route either through the
			// notification system. Operators who care read the run history.
			return "", false
		case model.ReasonStartFailed:
			// The give-up is announced by the dedicated EventServiceFatal →
			// KindServiceFatal notification. Suppress the run row here so the
			// FATAL transition rings the bell exactly once instead of pairing a
			// generic "failed" with the specific "gave up". The run row still
			// persists and streams over SSE for the history view.
			return "", false
		default:
			return KindRunFailed, true
		}
	default:
		// EventRunCreated, EventRunUpdated, EventLogLine: not surfaced as
		// notifications. Created is implicit in started/completed; updated
		// reflects intermediate state changes that would just be noise.
		return "", false
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
		if run.StartedAt != nil && run.EndedAt != nil {
			duration = fmt.Sprintf(" after %s", run.EndedAt.Sub(*run.StartedAt).Round(time.Second))
		}
		if run.ExitCode != 0 {
			return fmt.Sprintf("%s exit %d%s", *run.EndReason, run.ExitCode, duration)
		}
		return fmt.Sprintf("%s%s", *run.EndReason, duration)
	}
	return ""
}
