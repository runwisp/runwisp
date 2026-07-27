// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package cronspec is the single definition of the cron grammar RunWisp
// accepts. Every component that parses a task's cron expression — the
// scheduler, missed-tick catch-up, the TUI next-run preview, config
// validation, and the demo seeder — must build its parser here so the
// grammar can never drift between validation and execution.
package cronspec

import (
	"time"

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

// NewScheduleParser returns a cron.ScheduleParser for the RunWisp cron grammar
// whose schedules recover a wall-clock tick that lands inside a spring-forward
// DST gap (see dstGapSchedule). Every component that consults a schedule's Next
// — the cron engine and the jitter gap math — must build through this so the
// grammar and the DST recovery stay single-sourced. Validation (which only
// parses, never fires) keeps using NewParser directly.
func NewScheduleParser() cron.ScheduleParser {
	return scheduleParser{inner: NewParser()}
}

// scheduleParser delegates parsing to the RunWisp grammar and wraps the
// resulting wall-clock schedule so its Next recovers spring-forward gap ticks.
type scheduleParser struct {
	inner cron.Parser
}

func (p scheduleParser) Parse(spec string) (cron.Schedule, error) {
	sched, err := p.inner.Parse(spec)
	if err != nil {
		return nil, err
	}
	// Only field-based (wall-clock) specs can skip a tick in a DST gap. @every
	// (ConstantDelaySchedule) advances by a real duration and has no wall-clock
	// time to miss, so it passes through untouched.
	spec6, ok := sched.(*cron.SpecSchedule)
	if !ok {
		return sched, nil
	}
	return dstGapSchedule{inner: spec6}, nil
}

// dstGapSchedule wraps a SpecSchedule so a wall-clock tick that lands inside a
// spring-forward DST gap — a local time that never occurs because the clock
// jumps forward — fires at the gap end instead of vanishing. robfig/cron's Next
// steps the hour field straight across the gap (01:59 → 03:00) and never
// matches the missing hour, so "0 2 * * *" on a spring-forward day would
// otherwise wrap to the next day with no run and no record (breaking Prime
// Directive #1). We recover the tick by re-evaluating the spec on a gapless
// clock pinned to the pre-gap offset; if it matched a time inside the missing
// window, we return that instant — exactly the single valid UTC point time.Date
// yields when it normalizes the non-existent local time forward.
type dstGapSchedule struct {
	inner *cron.SpecSchedule
}

func (d dstGapSchedule) Next(from time.Time) time.Time {
	naive := d.inner.Next(from)
	if naive.IsZero() {
		return naive
	}

	// The location the spec is evaluated in: its own (CRON_TZ) when pinned,
	// else the location of the time the cron engine hands us — mirroring
	// SpecSchedule.Next's own time.Local fallback.
	loc := d.inner.Location
	if loc == time.Local {
		loc = from.Location()
	}

	// A clock-advancing (spring-forward) transition lies between from and naive
	// only if the UTC offset increased across them. Without one there is no gap
	// a tick could have fallen into. A fall-back (offset decreases) makes a wall
	// time repeat, not disappear, and is deduped in the scheduler instead.
	_, fromOff := from.In(loc).Zone()
	_, naiveOff := naive.In(loc).Zone()
	if naiveOff <= fromOff {
		return naive
	}

	// Re-evaluate on a gapless clock pinned to the pre-gap offset (from's), so a
	// tick in the missing window is matched instead of stepped over. The matched
	// instant equals what time.Date yields when it normalizes that non-existent
	// local time forward, so recovered is already the answer we'd return.
	fixed := *d.inner
	fixed.Location = time.FixedZone("dstgap", fromOff)
	recovered := fixed.Next(from.In(fixed.Location))
	if !recovered.Before(naive) {
		// The gapless match is no earlier than naive: naive didn't step over it,
		// so nothing was skipped (e.g. a tick at or after the gap end).
		return naive
	}

	// Confirm recovered's wall-clock time is genuinely non-existent in loc — that
	// it fell in the gap rather than being a valid earlier tick. time.Date
	// normalizes a non-existent local time forward, changing its fields; a real
	// one is reproduced unchanged.
	w := recovered.In(fixed.Location)
	norm := time.Date(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute(), w.Second(), w.Nanosecond(), loc)
	if norm.Hour() == w.Hour() && norm.Minute() == w.Minute() && norm.Second() == w.Second() {
		// The wall time exists; recovered is a real earlier tick the engine would
		// already have returned. Defensive — leave naive untouched.
		return naive
	}
	return recovered.In(loc)
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
