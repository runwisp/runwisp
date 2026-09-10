// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package notify is the in-process notification subsystem: it consumes run
// lifecycle events from the daemon's event bus, routes them through
// configurable predicates, renders provider-specific messages (Slack,
// Telegram, in-app), and surfaces outbound delivery failures back as in-app
// notifications. See plan: /apps/runwisp/internal/notify/notify.go for
// service-level lifecycle.
package notify

import (
	"fmt"
	"time"

	"github.com/runwisp/runwisp/internal/model"
)

// Severity classifies how attention-worthy an event is.
type Severity string

const (
	SevInfo  Severity = "info"
	SevWarn  Severity = "warn"
	SevError Severity = "error"
)

// Kind is the public name for an event class. Stable across releases (matches
// the SSE event type names exposed to clients).
type Kind string

const (
	KindRunStarted           Kind = "run.started"
	KindRunSucceeded         Kind = "run.succeeded"
	KindRunFailed            Kind = "run.failed"
	KindRunTimeout           Kind = "run.timeout"
	KindRunStopped           Kind = "run.stopped"
	KindRunCrashed           Kind = "run.crashed"
	KindRunMissed            Kind = "run.missed"
	KindServiceFatal         Kind = "service.fatal"
	KindLogDiskPressure      Kind = "log.disk_pressure"
	KindNotifyDeliveryFailed Kind = "notify.delivery_failed"
)

// Title returns the user-facing title fragment for this Kind.
func (k Kind) Title(ev *Event) string {
	switch k {
	case KindRunStarted:
		return fmt.Sprintf("%s started", ev.TaskName)
	case KindRunSucceeded:
		return fmt.Sprintf("%s succeeded", ev.TaskName)
	case KindRunFailed:
		return fmt.Sprintf("%s failed", ev.TaskName)
	case KindRunTimeout:
		return fmt.Sprintf("%s timed out", ev.TaskName)
	case KindRunStopped:
		return fmt.Sprintf("%s stopped", ev.TaskName)
	case KindRunCrashed:
		return fmt.Sprintf("%s crashed", ev.TaskName)
	case KindRunMissed:
		return fmt.Sprintf("%s missed", ev.TaskName)
	case KindServiceFatal:
		return fmt.Sprintf("%s gave up: instance keeps failing to start", ev.TaskName)
	case KindLogDiskPressure:
		return fmt.Sprintf("%s log output paused: low disk", ev.TaskName)
	case KindNotifyDeliveryFailed:
		channel := ""
		if ev.Extra != nil {
			if v, ok := ev.Extra["channel"].(string); ok {
				channel = v
			}
		}
		if channel != "" {
			return fmt.Sprintf("Delivery to %s failed", channel)
		}
		return "Notification delivery failed"
	default:
		return string(k)
	}
}

// Outcome returns the route-matching token for this event — the vocabulary a
// [[route]] match.kinds entry (and a task's `failures` list) uses. For a run
// event it is the fine-grained end reason (`failed`, `timeout`, `log_overflow`,
// `stopped`, `daemon_stopped`, `missed`, `succeeded`, …), which is strictly more
// precise than the collapsed SSE Kind (run.failed lumps failed+log_overflow).
// For the non-run events and the pre-terminal run.started it is the Kind string
// (`service.fatal`, `log.disk_pressure`, `started`).
func (ev *Event) Outcome() string {
	if ev == nil {
		return ""
	}
	switch ev.Kind {
	case KindServiceFatal, KindLogDiskPressure:
		return string(ev.Kind)
	case KindRunStarted:
		return "started"
	}
	if ev.Run != nil && ev.Run.EndReason != nil {
		return string(*ev.Run.EndReason)
	}
	return string(ev.Kind)
}

// FingerprintKey is the coalescing identity of an event: kind + task name +
// a discriminator (the run's end reason, or for delivery-failure events the
// failed channel + original kind). Two events that should fold together share
// this key. Both coalescers — the in-app dispatcher's and the routing-action
// window — derive their fold identity from this single function so their
// semantics can never drift apart (they previously had to be edited in
// lockstep). The in-app side hashes the bytes; the routing side uses the
// string directly.
func FingerprintKey(ev *Event) string {
	if ev == nil {
		return ""
	}
	extra := ""
	if ev.Run != nil && ev.Run.EndReason != nil {
		extra = string(*ev.Run.EndReason)
	}
	if ev.Kind == KindNotifyDeliveryFailed && ev.Extra != nil {
		ch, _ := ev.Extra["channel"].(string)
		ok, _ := ev.Extra["original_kind"].(string)
		extra = ch + "|" + ok
	}
	return string(ev.Kind) + "|" + ev.TaskName + "|" + extra
}

// Event is the public boundary type the notification subsystem operates on.
// It is constructed by bridge.go from events.RunEvent or by the dispatcher
// when synthesizing delivery-failure notifications.
type Event struct {
	ID        string
	Kind      Kind
	Severity  Severity
	Timestamp time.Time
	TaskName  string
	// IsFailure is the classified failure bit for this event: run events carry
	// the persisted run.IsFailure (the task's `failures` policy applied at
	// termination), service.fatal is always a failure, and informational events
	// (disk pressure, delivery-failed) are not. It is what the built-in failure
	// route matches on (MatchFailure) — never the raw Kind — so a task that
	// promotes `stopped` or demotes `missed` re-routes automatically.
	IsFailure bool
	Run       *model.Run
	// LogPath is the on-disk path of the captured output for this run. It is
	// sourced from the executor's event envelope (never persisted on the Run
	// row) and consumed by renderers that need to splice a tail of the log
	// into the notification body.
	LogPath string
	Reason  string
	Extra   map[string]any
}
