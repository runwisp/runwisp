// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package cronspec is the single definition of the cron grammar RunWisp
// accepts. Every component that parses a task's cron expression — the
// scheduler, missed-tick catch-up, the TUI next-run preview, config
// validation, and the demo seeder — must build its parser here so the
// grammar can never drift between validation and execution.
//
// go-cron already gives the grammar its vixie-cron edges: day-of-week 7 is
// Sunday, a tick inside a spring-forward DST gap fires at the gap end, and
// @every rejects intervals below 1s.
package cronspec

import (
	"errors"
	"strconv"
	"strings"

	cron "github.com/netresearch/go-cron"
)

// parseOptions is the grammar accepted for task cron expressions: 5-field
// specs (minute hour dom month dow) plus an optional leading seconds field
// (6-field: second minute hour dom month dow) plus descriptors like @hourly,
// @daily, and @every 1h30m. SecondOptional (not Second) keeps 5-field specs
// valid: they fire at :00. DowOrDom keeps vixie's rule that a restricted
// day-of-month and day-of-week are ORed (go-cron ANDs them by default).
const parseOptions = cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor | cron.DowOrDom

// NewParser returns a parser for the RunWisp cron grammar.
func NewParser() cron.ScheduleParser {
	return specParser{inner: cron.NewParser(parseOptions)}
}

// specParser is go-cron's parser plus the vixie behaviours it only offers
// behind options that are still in review upstream. Each patch below names the
// PR that replaces it; once all three land this type goes away and NewParser
// returns cron.NewParser directly.
type specParser struct {
	inner cron.Parser
}

func (p specParser) Parse(spec string) (cron.Schedule, error) {
	sched, err := p.inner.Parse(p.lenientSteps(spec))
	if err != nil {
		return nil, err
	}
	if s, ok := sched.(*cron.SpecSchedule); ok {
		markStarDays(s, spec)
		if neverFires(s) {
			return nil, errors.New("schedule never fires (the day of month does not exist in the given months)")
		}
	}
	return sched, nil
}

// fieldMin is each field's lowest value, in 6-field order.
var fieldMin = [6]string{"0", "0", "0", "1", "1", "0"}

// lenientSteps rewrites every step at or above its range size (*/60, 7/2) to the
// range start, which is all such a step ever selects. go-cron rejects these;
// vixie cron and robfig/cron accepted them, so configs that load today must keep
// loading. go-cron itself decides which terms are oversized: a term qualifies
// when it fails to parse but parses once its step is dropped.
//
// Replace with cron.LenientSteps once netresearch/go-cron#419 lands.
func (p specParser) lenientSteps(spec string) string {
	fields := strings.Fields(spec)
	body := fieldsAfterTZ(spec)
	if len(body) != 5 && len(body) != 6 {
		return spec
	}
	offset := len(fields) - len(body)
	for i, field := range body {
		pos := i + 6 - len(body)
		terms := strings.Split(field, ",")
		for j, term := range terms {
			terms[j] = p.lenientTerm(pos, term)
		}
		fields[offset+i] = strings.Join(terms, ",")
	}
	return strings.Join(fields, " ")
}

// lenientTerm returns term's range start when its step is oversized for field
// pos (6-field order), else term unchanged.
func (p specParser) lenientTerm(pos int, term string) string {
	base, step, ok := strings.Cut(term, "/")
	if n, err := strconv.Atoi(step); !ok || err != nil || n < 1 {
		return term
	}
	if p.probe(pos, term) == nil || p.probe(pos, base) != nil {
		return term
	}
	lo, _, _ := strings.Cut(base, "-")
	if lo == "*" || lo == "?" {
		return fieldMin[pos]
	}
	return lo
}

// probe parses term alone in field pos of an otherwise all-star 6-field spec.
func (p specParser) probe(pos int, term string) error {
	six := []string{"*", "*", "*", "*", "*", "*"}
	six[pos] = term
	_, err := p.inner.Parse(strings.Join(six, " "))
	return err
}

// daysIn is the longest each month gets, February counting its leap day.
var daysIn = [13]uint{0, 31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}

// neverFires reports a spec like "0 0 30 2 *", which go-cron parses but never
// matches: the day of month exists in none of the months, and day-of-week
// can't stand in for it because the two are ANDed (one carries starBit). It
// reads the fields instead of asking Next, which gives up after five years
// and so would also reject rare but real dates like "0 9 25 12 */7".
//
// Replace with cron.StrictDays once netresearch/go-cron#421 lands.
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

// starBit is go-cron's unexported "field was *" flag (spec.go). Under DowOrDom,
// dayMatches ANDs day-of-month with day-of-week when either carries it, and ORs
// them when neither does.
const starBit = 1 << 63

// markStarDays restores vixie/cronie's day-matching rule for stepped stars.
// Traditional cron sets DOM_STAR / DOW_STAR whenever the field's first character
// is '*', so "*/2" still counts as unrestricted and "0 0 */2 * 1" means odd
// days that are also Mondays. go-cron clears the flag for any step above 1,
// which turns that into odd days OR Mondays. '?' is an alias for '*' and is
// treated the same way.
//
// Replace with cron.StepWildcard once netresearch/go-cron#420 lands.
func markStarDays(s *cron.SpecSchedule, spec string) {
	body := fieldsAfterTZ(spec)
	if len(body) != 5 && len(body) != 6 {
		return
	}
	// Both sit at the same offset from the end of the 5-field and the
	// seconds-prefixed 6-field form.
	dom, dow := body[len(body)-3], body[len(body)-1]
	if strings.HasPrefix(dom, "*") || strings.HasPrefix(dom, "?") {
		s.Dom |= starBit
	}
	if strings.HasPrefix(dow, "*") || strings.HasPrefix(dow, "?") {
		s.Dow |= starBit
	}
}

// fieldsAfterTZ splits spec into fields, dropping a leading TZ= / CRON_TZ=
// prefix the way go-cron does before it counts fields.
func fieldsAfterTZ(spec string) []string {
	fields := strings.Fields(spec)
	if len(fields) > 0 && (strings.HasPrefix(fields[0], "TZ=") || strings.HasPrefix(fields[0], "CRON_TZ=")) {
		return fields[1:]
	}
	return fields
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
