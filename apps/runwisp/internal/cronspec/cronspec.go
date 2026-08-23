// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package cronspec is the single definition of the cron grammar RunWisp
// accepts. Every component that parses a task's cron expression — the
// scheduler, missed-tick catch-up, the TUI next-run preview, config
// validation, and the demo seeder — must build its parser here so the
// grammar can never drift between validation and execution.
package cronspec

import (
	"slices"
	"strconv"
	"strings"
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
//
// It hands back a cron.ScheduleParser rather than robfig's concrete cron.Parser
// so every spec goes through sundayAliased first: robfig bounds day-of-week at
// 0-6, and traditional cron accepts 0-7 with both ends meaning Sunday. A
// caller holding a bare cron.Parser would parse around that.
func NewParser() cron.ScheduleParser {
	return specParser{inner: cron.NewParser(ParseOptions)}
}

// specParser is the RunWisp grammar: robfig's parser with the day-of-week field
// normalized on the way in.
type specParser struct {
	inner cron.Parser
}

func (p specParser) Parse(spec string) (cron.Schedule, error) {
	return p.inner.Parse(sundayAliased(spec))
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
	inner cron.ScheduleParser
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

// sundayAliased rewrites the day-of-week field so 7 means Sunday, the vixie-cron
// convention robfig/cron does not implement (its dow bounds are 0-6, and a 7
// fails with "end of range (7) above maximum (6)").
//
// This is not a nicety: Debian and Ubuntu's own /etc/crontab ships
// `47 6 * * 7 root … run-parts /etc/cron.weekly`, so without this a stock box's
// weekly housekeeping is dropped the moment RunWisp reads its crontabs.
//
// Only the last field of a 5- or 6-field spec is touched, so a `*/7` step
// elsewhere, a minute or month value of 7, and every @descriptor pass through
// unchanged. An unrecognized field count is left alone for robfig to reject.
func sundayAliased(spec string) string {
	fields := strings.Fields(spec)
	body := fields
	// robfig peels a TZ= / CRON_TZ= prefix off before counting fields, so it must
	// not be counted here either.
	if len(body) > 0 && (strings.HasPrefix(body[0], "TZ=") || strings.HasPrefix(body[0], "CRON_TZ=")) {
		body = body[1:]
	}
	if len(body) != 5 && len(body) != 6 {
		return spec
	}
	// dow is the last field of both the 5-field and the seconds-prefixed 6-field
	// form, so no index arithmetic can get the position wrong.
	last := len(fields) - 1
	dow := dowField(fields[last])
	if dow == fields[last] {
		return spec
	}
	out := slices.Clone(fields)
	out[last] = dow
	return strings.Join(out, " ")
}

// dowField rewrites one day-of-week field, term by comma-separated term.
func dowField(field string) string {
	if !strings.Contains(field, "7") {
		return field
	}
	terms := strings.Split(field, ",")
	for i, term := range terms {
		terms[i] = dowTerm(term)
	}
	return strings.Join(terms, ",")
}

// dowTerm rewrites one term of a day-of-week field: a value, a range, either
// with an optional step.
func dowTerm(term string) string {
	base, step, hasStep := strings.Cut(term, "/")
	lo, hi, isRange := strings.Cut(base, "-")
	if !isRange && base == "7" {
		// Bare 7 is Sunday, and 7 is already vixie's own max for this field, so no
		// attached step can ever add a value beyond Sunday itself — drop it rather
		// than pass it through to robfig, whose max of 6 would make "0/N" wrap
		// around into other days.
		return "0"
	}
	if isRange && lo == "7" && hi != "7" {
		// A range starting at 7: the value is Sunday, so 0 says the same thing in
		// robfig's bounds.
		return "0" + term[1:]
	}
	if !isRange || hi != "7" {
		// A named day, a 7 that is only a step (*/7), or no 7 in a value position.
		return term
	}
	// A range ending at 7 has to be expanded rather than clamped. vixie folds day 7
	// into day 0 *after* expanding the range, so `1-7` is the whole week — clamping
	// it to `1-0` would be an inverted range robfig rejects, and `0-7` clamped to
	// `0-0` would silently shrink every day to Sunday.
	return expandDowRangeTo7(term, lo, step, hasStep)
}

// expandDowRangeTo7 expands a day-of-week range that ends at 7 (Sunday) into an
// explicit comma list, folding day 7 to day 0. It returns term unchanged if lo
// or the step can't be parsed.
func expandDowRangeTo7(term, lo, step string, hasStep bool) string {
	from, err := strconv.Atoi(lo)
	if err != nil {
		return term
	}
	by := 1
	if hasStep {
		if by, err = strconv.Atoi(step); err != nil || by < 1 {
			return term
		}
	}
	var days []string
	var seen [7]bool
	for v := from; v >= 0 && v <= 7; v += by {
		if d := v % 7; !seen[d] {
			seen[d] = true
			days = append(days, strconv.Itoa(d))
		}
	}
	if len(days) == 0 {
		return term
	}
	return strings.Join(days, ",")
}
