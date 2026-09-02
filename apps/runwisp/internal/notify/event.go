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
	KindUpdateAvailable      Kind = "update.available"
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
	case KindUpdateAvailable:
		if ev.Extra != nil {
			if v, ok := ev.Extra["latest_version"].(string); ok && v != "" {
				return fmt.Sprintf("RunWisp %s is available", v)
			}
		}
		return "A new RunWisp release is available"
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
	// Key update-available rows by version so each release is its own row: a
	// newer version surfaces even after the previous one was read, while
	// re-detecting the same version stays a single stable fingerprint.
	if ev.Kind == KindUpdateAvailable && ev.Extra != nil {
		v, _ := ev.Extra["latest_version"].(string)
		extra = v
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
	Run       *model.Run
	// LogPath is the on-disk path of the captured output for this run. It is
	// sourced from the executor's event envelope (never persisted on the Run
	// row) and consumed by renderers that need to splice a tail of the log
	// into the notification body.
	LogPath string
	Reason  string
	Extra   map[string]any
}
