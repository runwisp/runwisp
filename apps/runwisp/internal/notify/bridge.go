// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
)

// MapRunEvent converts an internal events.Event carrying a RunEvent payload
// into a notify.Event. Returns nil when the event is not run-shaped (log
// lines, etc.) — the caller should drop those silently.
func MapRunEvent(e events.Event) *Event {
	re, ok := e.Data.(events.RunEvent)
	if !ok || re.Run == nil {
		return nil
	}
	kind, severity, ok := mapRunEventType(e.Type, re.Run)
	if !ok {
		return nil
	}
	return &Event{
		ID:        ulid.Make().String(),
		Kind:      kind,
		Severity:  severity,
		Timestamp: e.Timestamp,
		TaskName:  re.Run.TaskName,
		Run:       re.Run,
		Reason:    runReasonString(re.Run, re.Error),
	}
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
		case model.ReasonFailed:
			return KindRunFailed, SevError, true
		case model.ReasonTimeout:
			return KindRunTimeout, SevError, true
		case model.ReasonStopped:
			return KindRunStopped, SevWarn, true
		case model.ReasonCrashed:
			return KindRunCrashed, SevError, true
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
