// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package cronspec is the single definition of the cron grammar RunWisp
// accepts. Every component that parses a task's cron expression — the
// scheduler, missed-tick catch-up, the TUI next-run preview, config
// validation, and the demo seeder — must build its parser here so the
// grammar can never drift between validation and execution.
package cronspec

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// parseOptions is the grammar accepted for task cron expressions: 5-field
// specs (minute hour dom month dow) plus an optional leading seconds field
// (6-field: second minute hour dom month dow) plus descriptors like @hourly,
// @daily, and @every 1h30m. SecondOptional (not Second) keeps 5-field specs
// valid: robfig prepends a "0" seconds field, so they fire at :00.
const parseOptions = cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor

// NewParser returns a parser for the RunWisp cron grammar.
//
// It hands back a cron.ScheduleParser rather than robfig's concrete cron.Parser
// so every spec goes through sundayAliased first: robfig bounds day-of-week at
// 0-6, and traditional cron accepts 0-7 with both ends meaning Sunday. A
// caller holding a bare cron.Parser would parse around that.
func NewParser() cron.ScheduleParser {
	return specParser{inner: cron.NewParser(parseOptions)}
}

// specParser is the RunWisp grammar: robfig's parser with the day-of-week field
// normalized on the way in.
type specParser struct {
	inner cron.Parser
}

func (p specParser) Parse(spec string) (cron.Schedule, error) {
	sched, err := p.inner.Parse(sundayAliased(spec))
	if err != nil {
		return nil, err
	}
	if every, ok := everyDuration(spec); ok && every < time.Second {
		return nil, fmt.Errorf("@every interval %s is below the 1s minimum", every)
	}
	if s, ok := sched.(*cron.SpecSchedule); ok {
		markStarDays(s, spec)
		if neverFires(s) {
			return nil, errors.New("schedule never fires (the day of month does not exist in the given months)")
		}
	}
	return sched, nil
}

// daysIn is the longest each month gets, February counting its leap day.
var daysIn = [13]uint{0, 31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}

// neverFires reports a spec like "0 0 30 2 *", which robfig parses but never
// matches: the day of month exists in none of the months, and day-of-week
// can't stand in for it because the two are ANDed (one carries starBit). It
// reads the fields instead of asking Next, which gives up after five years
// and so would also reject rare but real dates like "0 9 25 12 */7".
func neverFires(s *cron.SpecSchedule) bool {
	if s.Dom&starBit != 0 || s.Dow&starBit == 0 {
		return false
	}
	for month := uint(1); month <= 12; month++ {
		if s.Month&(1<<month) == 0 {
			continue
		}
		for day := uint(1); day <= daysIn[month]; day++ {
			if s.Dom&(1<<day) != 0 {
				return false
			}
		}
	}
	return true
}

// starBit is robfig/cron's unexported "field was *" flag (spec.go). dayMatches
// ANDs day-of-month with day-of-week when either carries it, and ORs them when
// neither does.
const starBit = 1 << 63

// markStarDays restores vixie/cronie's day-matching rule for stepped stars.
// Traditional cron sets DOM_STAR / DOW_STAR whenever the field's first character
// is '*', so "*/2" still counts as unrestricted and "0 0 */2 * 1" means odd
// days that are also Mondays. robfig clears the flag for any step above 1, which
// turned that into odd days OR Mondays. '?' is robfig's alias for '*' and is
// treated the same way.
func markStarDays(s *cron.SpecSchedule, spec string) {
	dom, dow, ok := dayFields(spec)
	if !ok {
		return
	}
	if strings.HasPrefix(dom, "*") || strings.HasPrefix(dom, "?") {
		s.Dom |= starBit
	}
	if strings.HasPrefix(dow, "*") || strings.HasPrefix(dow, "?") {
		s.Dow |= starBit
	}
}

// dayFields returns the day-of-month and day-of-week fields of a 5- or 6-field
// spec. robfig peels a TZ= / CRON_TZ= prefix off before counting fields, so it
// is skipped here too. ok is false for descriptors and wrong field counts.
func dayFields(spec string) (dom, dow string, ok bool) {
	body := fieldsAfterTZ(spec)
	if len(body) != 5 && len(body) != 6 {
		return "", "", false
	}
	// Both sit at the same offset from the end of the 5-field and the
	// seconds-prefixed 6-field form.
	return body[len(body)-3], body[len(body)-1], true
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

// everyDuration returns the interval of an "@every <duration>" spec (after an
// optional TZ= / CRON_TZ= prefix). robfig's ConstantDelaySchedule has already
// rounded a zero or sub-second interval up to 1s by the time Parse returns, so
// the original text is the only place to reject it.
func everyDuration(spec string) (time.Duration, bool) {
	fields := fieldsAfterTZ(spec)
	if len(fields) != 2 || fields[0] != "@every" {
		return 0, false
	}
	d, err := time.ParseDuration(fields[1])
	return d, err == nil
}

// fieldsAfterTZ splits spec into fields, dropping a leading TZ= / CRON_TZ=
// prefix the way robfig does before it counts fields.
func fieldsAfterTZ(spec string) []string {
	fields := strings.Fields(spec)
	if len(fields) > 0 && (strings.HasPrefix(fields[0], "TZ=") || strings.HasPrefix(fields[0], "CRON_TZ=")) {
		return fields[1:]
	}
	return fields
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
	_, dow, ok := dayFields(spec)
	if !ok {
		return spec
	}
	aliased := dowField(dow)
	if aliased == dow {
		return spec
	}
	fields := strings.Fields(spec)
	fields[len(fields)-1] = aliased
	return strings.Join(fields, " ")
}

// dowField rewrites one day-of-week field, term by comma-separated term.
// Every term is inspected: a bare "N/step" reaches vixie's implicit field max
// of 7 (Sunday) without the field text ever containing a literal "7", so a
// "contains 7" shortcut here would silently drop that Sunday occurrence.
func dowField(field string) string {
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
	if !isRange && hasStep {
		// A bare "N/step" (no explicit high). vixie defaults the implicit upper
		// bound to the field max — 7 for day-of-week — so "1/2" is 1,3,5,7(=Sun).
		// robfig would cap at its own max of 6 and drop the Sunday occurrence, so
		// expand against 7 the same way an explicit "N-7/step" range is handled.
		// "*" and named days don't parse as an int and fall through unchanged.
		if _, err := strconv.Atoi(base); err == nil {
			return expandDowRangeTo7(term, base, step, true)
		}
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

var dowNames = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

// expandDowRangeTo7 expands a day-of-week range that ends at 7 (Sunday) into an
// explicit comma list, folding day 7 to day 0. It returns term unchanged if lo
// or the step can't be parsed.
func expandDowRangeTo7(term, lo, step string, hasStep bool) string {
	from, err := strconv.Atoi(lo)
	if err != nil {
		// A named start ("sun-7", "fri-7") means the same as its number.
		var ok bool
		if from, ok = dowNames[strings.ToLower(lo)]; !ok {
			return term
		}
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
