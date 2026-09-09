// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package kinds is a leaf package containing only the canonical string values
// for notification event kinds. It exists so both internal/config (TOML
// validation) and internal/notify (typed Kind enum) can share a single
// source of truth without an import cycle.
package kinds

// AllKindStrings lists the event kinds a [[notify.routes]] rule may match on.
// DeliveryFailedKind is deliberately excluded: it bypasses the route engine
// entirely (see DeliveryFailedKind), so a rule matching on it would validate
// but could never fire.
var AllKindStrings = []string{
	"run.started",
	"run.succeeded",
	"run.failed",
	"run.timeout",
	"run.stopped",
	"run.crashed",
	"run.missed",
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

var AllSeverityStrings = []string{"info", "warn", "error"}
