// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package kinds is a leaf package containing only the canonical string values
// for notification event kinds. It exists so both internal/config (TOML
// validation) and internal/notify (typed Kind enum) can share a single
// source of truth without an import cycle.
package kinds

// AllKindStrings lists the tokens a [[route]] match.kinds entry may use. They
// are the outcome vocabulary shared with a task's `failures` list — bare
// end-reason names — plus the non-run event tokens and the pre-terminal
// `started`. It stays in sync with notify.Event.Outcome().
//
// Deliberately excluded, so a route can't be written that never fires:
// `skipped`/`dst_skipped` (the scheduler doing its job, never notified),
// `start_failed` (announced via `service.fatal` instead), and
// DeliveryFailedKind (bypasses the route engine, straight to the bell).
var AllKindStrings = []string{
	"started",
	"succeeded",
	"failed",
	"timeout",
	"crashed",
	"log_overflow",
	"queue_full",
	"stopped",
	"daemon_stopped",
	"missed",
	"service.fatal",
	"log.disk_pressure",
}

// DeliveryFailedKind is the synthetic event a permanently-failed delivery
// raises. It goes straight to the in-app bell as a cycle guard against a
// route that could itself trigger more delivery failures, never through the
// route engine — see internal/config/notify_schema.go's validateRoute, which
// rejects it in match.kinds with a message pointing at this instead of the
// less clear "not a valid kind".
const DeliveryFailedKind = "notify.delivery_failed"
