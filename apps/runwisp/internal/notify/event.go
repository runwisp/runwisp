// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
	KindNotifyDeliveryFailed Kind = "notify.delivery_failed"
)

// AllKinds is the canonical enumeration; useful for validation in configload.
var AllKinds = []Kind{
	KindRunStarted,
	KindRunSucceeded,
	KindRunFailed,
	KindRunTimeout,
	KindRunStopped,
	KindRunCrashed,
	KindNotifyDeliveryFailed,
}

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

// FingerprintToken returns the stable token used by the coalescer to bucket
// events of this Kind. Must be updated when AllKinds grows.
func (k Kind) FingerprintToken() string {
	switch k {
	case KindRunStarted,
		KindRunSucceeded,
		KindRunFailed,
		KindRunTimeout,
		KindRunStopped,
		KindRunCrashed,
		KindNotifyDeliveryFailed:
		return string(k)
	default:
		return string(k)
	}
}

// AllSeverities mirrors AllKinds for severity validation.
var AllSeverities = []Severity{SevInfo, SevWarn, SevError}

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
	Reason    string
	Extra     map[string]any
}
