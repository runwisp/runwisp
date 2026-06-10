// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package cronspec is the single definition of the cron grammar RunWisp
// accepts. Every component that parses a task's cron expression — the
// scheduler, missed-tick catch-up, the TUI next-run preview, config
// validation, and the demo seeder — must build its parser here so the
// grammar can never drift between validation and execution.
package cronspec

import (
	"github.com/robfig/cron/v3"
)

// ParseOptions is the grammar accepted for task cron expressions: 5-field
// specs (minute hour dom month dow) plus an optional leading seconds field
// (6-field: second minute hour dom month dow) plus descriptors like @hourly,
// @daily, and @every 1h30m. The seconds field is enabled via SecondOptional
// rather than Second so existing 5-field specs keep parsing unchanged —
// robfig prepends a "0" seconds field to them, firing at :00 exactly as
// before. This is a superset of robfig/cron's standard parser; kept explicit
// so the contract is load-bearing.
const ParseOptions = cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor

// NewParser returns a parser for the RunWisp cron grammar.
func NewParser() cron.Parser {
	return cron.NewParser(ParseOptions)
}

// Validate reports whether spec parses under the RunWisp cron grammar,
// evaluated exactly as the scheduler will at boot: when timezone is
// non-empty it is prepended as a CRON_TZ= prefix, so an invalid task
// timezone fails here the same way it would fail scheduling.
func Validate(spec, timezone string) error {
	full := spec
	if timezone != "" {
		full = "CRON_TZ=" + timezone + " " + spec
	}
	_, err := NewParser().Parse(full)
	return err
}
