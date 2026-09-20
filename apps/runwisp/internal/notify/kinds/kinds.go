// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package kinds is a leaf package containing only the canonical string values
// for notification event kinds. It exists so both internal/config (TOML
// validation) and internal/notify (typed Kind enum) can share a single
// source of truth without an import cycle.
package kinds

import "github.com/runwisp/runwisp/internal/model"

// excludedEndReasons are model.EndReason values deliberately left out of
// AllKindStrings so a route can't be written that never fires: `skipped`/
// `dst_skipped` (the scheduler doing its job, never notified) and
// `start_failed` (announced via `service.fatal` instead).
var excludedEndReasons = map[model.EndReason]struct{}{
	model.ReasonSkipped:     {},
	model.ReasonDSTSkipped:  {},
	model.ReasonStartFailed: {},
}

// logDiskPressureKind is the log.disk_pressure token, named once here so
// NeverFailureKinds below doesn't duplicate the literal by hand.
const logDiskPressureKind = "log.disk_pressure"

// AllKindStrings lists the tokens a [[route]] match.kinds entry may use. They
// are the outcome vocabulary shared with a task's `failures` list — every
// model.EndReason except excludedEndReasons — plus the non-run event tokens
// and the pre-terminal `started`. It stays in sync with notify.Event.Outcome()
// by construction: both derive from model.AllEndReasons.
//
// DeliveryFailedKind is separately excluded (see its own doc comment): it
// bypasses the route engine, straight to the bell.
var AllKindStrings = func() []string {
	out := make([]string, 0, len(model.AllEndReasons)+3)
	out = append(out, "started")
	for _, r := range model.AllEndReasons {
		if _, excluded := excludedEndReasons[r]; !excluded {
			out = append(out, string(r))
		}
	}
	return append(out, "service.fatal", logDiskPressureKind)
}()

// DeliveryFailedKind is the synthetic event a permanently-failed delivery
// raises. It goes straight to the in-app bell as a cycle guard against a
// route that could itself trigger more delivery failures, never through the
// route engine — see internal/config/notify_schema.go's validateRoute, which
// rejects it in match.kinds with a message pointing at this instead of the
// less clear "not a valid kind".
const DeliveryFailedKind = "notify.delivery_failed"

// NeverFailureKinds are the match.kinds tokens whose event can never carry
// the classified-failure bit, no matter how a task's `failures` policy is
// configured: "started" fires before a run has an outcome, "succeeded"
// is unconditionally excluded from failure classification (model.Task's
// IsFailureReason short-circuits ReasonSuccess), and "log.disk_pressure"
// events are hardcoded to IsFailure: false in bridge.go's MapEvent. Combined
// with match.failure = true, any one of these ANDs down to a route that can
// never fire — see validateRoute.
var NeverFailureKinds = []string{"started", string(model.ReasonSuccess), logDiskPressureKind}
