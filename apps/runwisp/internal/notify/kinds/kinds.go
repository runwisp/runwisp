// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package kinds is a leaf package containing only the canonical string values
// for notification event kinds. It exists so both internal/config (TOML
// validation) and internal/notify (typed Kind enum) can share a single
// source of truth without an import cycle.
package kinds

var AllKindStrings = []string{
	"run.started",
	"run.succeeded",
	"run.failed",
	"run.timeout",
	"run.stopped",
	"run.crashed",
	"notify.delivery_failed",
}

var AllSeverityStrings = []string{"info", "warn", "error"}
