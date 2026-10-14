// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package cronspec is the single definition of the cron grammar RunWisp
// accepts. Every component that parses a task's cron expression — the
// scheduler, missed-tick catch-up, the TUI next-run preview, config
// validation, and the demo seeder — must build its parser here so the
// grammar can never drift between validation and execution.
//
// go-cron already gives the grammar its vixie-cron edges: day-of-week 7 is
// Sunday and @every rejects intervals below 1s; RunWisp also rejects one that
// isn't a whole number of seconds (see fractionalEvery). A tick inside a spring-forward
// DST gap fires once at the first valid instant after it (see dstGapSchedule).
package cronspec

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

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
// behind options that are still in review upstream (each such patch names the
// PR that replaces it), plus the rejections, day-of-week wrap and DST-gap
// recovery RunWisp's documented grammar needs on top.
type specParser struct {
	inner cron.Parser
}

func (p specParser) Parse(spec string) (cron.Schedule, error) {
	if err := rangeStartAboveMax(spec); err != nil {
		return nil, err
	}
	sched, err := p.inner.Parse(p.lenientSteps(wrappedDowRanges(spec)))
	if err != nil {
		return nil, err
	}
	if cron.IsTriggered(sched) {
		// go-cron's @manual / @triggered / @none never fire on their own: their
		// Next is the zero time, which callers would read as a tick in year 1.
		// A task that only runs on demand simply has no cron.
		return nil, fmt.Errorf("%q never fires on its own; leave cron unset for a task that only runs on demand",
			strings.Join(fieldsAfterTZ(spec), " "))
	}
	if s, ok := sched.(*cron.SpecSchedule); ok {
		markStarDays(s, spec)
		if neverFires(s) {
			return nil, errors.New("schedule never fires (the day of month does not exist in the given months)")
		}
		return dstGapSchedule{inner: s}, nil
	}
	if err := fractionalEvery(spec); err != nil {
		return nil, err
	}
	return sched, nil
}

// fractionalEvery rejects an @every interval that isn't a whole number of
// seconds ("@every 1500ms"). go-cron truncates it to the second, so the task
// would fire at an interval the config doesn't say.
func fractionalEvery(spec string) error {
	body := fieldsAfterTZ(spec)
	if len(body) != 2 || body[0] != "@every" {
		return nil
	}
	if d, err := time.ParseDuration(body[1]); err == nil && d%time.Second != 0 {
		return fmt.Errorf("@every %s: the interval must be a whole number of seconds", body[1])
	}
	return nil
}

// fieldMax is each field's highest value, in 6-field order (day-of-week 7 is
// Sunday).
var fieldMax = [6]int{59, 59, 23, 31, 12, 7}

// rangeStartAboveMax rejects a range whose start is past its field's maximum
// ("30-2" in the hour field). go-cron reads any start above the end as a
// wrap-around and only bounds-checks the end, so it would accept these and fire
// on folded-over values.
func rangeStartAboveMax(spec string) error {
	body := fieldsAfterTZ(spec)
	if len(body) != 5 && len(body) != 6 {
		return nil
	}
	for i, field := range body {
		pos := i + 6 - len(body)
		for _, term := range strings.Split(field, ",") {
			base, _, _ := strings.Cut(term, "/")
			lo, _, isRange := strings.Cut(base, "-")
			if n, err := strconv.Atoi(lo); err == nil && isRange && n > fieldMax[pos] {
				return fmt.Errorf("beginning of range (%d) above maximum (%d): %q", n, fieldMax[pos], term)
			}
		}
	}
	return nil
}

// wrappedDowRanges rewrites every wrap-around day-of-week range (FRI-MON,
// 7-3/2) into an explicit day list. go-cron wraps day-of-week over 0-7, so
// Sunday sits in the cycle twice and a stepped wrap lands on the wrong days
// (FRI-MON/2 came out Fri,Sun,Mon). The list walks the 7-day week instead,
// counting 7 as Sunday. Only the last field is touched; a term that doesn't
// parse is left for go-cron to reject.
func wrappedDowRanges(spec string) string {
	body := fieldsAfterTZ(spec)
	if len(body) != 5 && len(body) != 6 {
		return spec
	}
	fields := strings.Fields(spec)
	terms := strings.Split(fields[len(fields)-1], ",")
	for i, term := range terms {
		terms[i] = wrappedDowTerm(term)
	}
	fields[len(fields)-1] = strings.Join(terms, ",")
	return strings.Join(fields, " ")
}

var dowNames = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

// dowValue parses one day-of-week bound: 0-7 or a three-letter name.
func dowValue(s string) (int, bool) {
	if n, err := strconv.Atoi(s); err == nil {
		return n, n >= 0 && n <= 7
	}
	n, ok := dowNames[strings.ToLower(s)]
	return n, ok
}

// wrappedDowTerm expands one "lo-hi[/step]" term whose lo is above hi, and
// returns any other term unchanged.
func wrappedDowTerm(term string) string {
	base, stepText, hasStep := strings.Cut(term, "/")
	loText, hiText, isRange := strings.Cut(base, "-")
	lo, okLo := dowValue(loText)
	hi, okHi := dowValue(hiText)
	if !isRange || !okLo || !okHi || lo <= hi {
		return term
	}
	step := 1
	if hasStep {
		var err error
		if step, err = strconv.Atoi(stepText); err != nil || step < 1 {
			return term
		}
	}
	lo %= 7
	if hi < lo {
		hi += 7
	}
	var days []string
	for d := lo; d <= hi; d += step {
		days = append(days, strconv.Itoa(d%7))
	}
	return strings.Join(days, ",")
}

// dstGapSchedule wraps a SpecSchedule so a wall-clock tick that lands inside a
// spring-forward DST gap (a local time that never occurs because the clock
// jumps forward) fires once at the first valid instant after the gap instead
// of vanishing. go-cron only recovers an hour the clock jumps over by exactly
// one (h → h+2): a gap at midnight ("0 0 * * *" in America/Santiago) or a
// 30-minute shift (Australia/Lord_Howe) would wrap to the next matching day
// with no run and no record. The spec is re-evaluated on a gapless clock pinned
// to the pre-gap offset; a match inside the missing window is exactly the
// instant time.Date yields when it normalizes that non-existent local time
// forward.
type dstGapSchedule struct {
	inner *cron.SpecSchedule
}

func (d dstGapSchedule) Next(from time.Time) time.Time {
	naive := d.inner.Next(from)
	if naive.IsZero() {
		return naive
	}

	// The location the spec is evaluated in: its own (CRON_TZ) when pinned,
	// else the location of the time handed in, mirroring SpecSchedule.Next's
	// own time.Local fallback.
	loc := d.inner.Location
	if loc == time.Local {
		loc = from.Location()
	}

	// A spring-forward transition lies between from and naive only if the UTC
	// offset increased across them. A fall-back makes a wall time repeat, not
	// disappear, and is deduped in the scheduler instead.
	_, fromOff := from.In(loc).Zone()
	_, naiveOff := naive.In(loc).Zone()
	if naiveOff <= fromOff {
		return naive
	}

	fixed := *d.inner
	fixed.Location = time.FixedZone("dstgap", fromOff)
	recovered := fixed.Next(from.In(fixed.Location))
	if recovered.IsZero() || !recovered.Before(naive) {
		return naive
	}

	// Only a wall time that is genuinely missing in loc counts: time.Date
	// normalizes a non-existent local time forward, changing its fields, and
	// reproduces a real one unchanged.
	w := recovered.In(fixed.Location)
	norm := time.Date(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute(), w.Second(), w.Nanosecond(), loc)
	if norm.Hour() == w.Hour() && norm.Minute() == w.Minute() && norm.Second() == w.Second() {
		return naive
	}
	return recovered.In(loc)
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

// Fields are the values each field of a cron spec matches, ascending. Day of
// week runs 0-6 with Sunday as 0, however the spec wrote it.
type Fields struct {
	Second     []int `json:"second"`
	Minute     []int `json:"minute"`
	Hour       []int `json:"hour"`
	DayOfMonth []int `json:"dayOfMonth"`
	Month      []int `json:"month"`
	DayOfWeek  []int `json:"dayOfWeek"`
}

// FieldError says why one field of a spec doesn't parse. Field is
// "expression" when the fault is the spec as a whole: its field count, an
// unknown descriptor, a TZ= prefix, an @every interval.
type FieldError struct {
	Field   string `json:"field"`
	Value   string `json:"value,omitempty"`
	Message string `json:"message"`
}

// fieldNames name the fields of a 6-field spec; a 5-field one starts at minute.
var fieldNames = [6]string{"second", "minute", "hour", "dayOfMonth", "month", "dayOfWeek"}

// Explain parses spec as Validate does and returns the values each field
// matches, or why it doesn't parse: one error per faulty field, else one for
// the whole spec. fields is nil for @every, which has none.
func Explain(spec string) (*Fields, []FieldError) {
	sched, err := NewParser().Parse(spec)
	if err != nil {
		return nil, fieldErrors(spec, err)
	}
	gap, ok := sched.(dstGapSchedule)
	if !ok {
		return nil, nil
	}
	s := gap.inner
	return &Fields{
		Second:     bitValues(s.Second, 0, 59),
		Minute:     bitValues(s.Minute, 0, 59),
		Hour:       bitValues(s.Hour, 0, 23),
		DayOfMonth: bitValues(s.Dom, 1, 31),
		Month:      bitValues(s.Month, 1, 12),
		DayOfWeek:  bitValues(s.Dow, 0, 6),
	}, nil
}

// fieldErrors parses each field of spec on its own, the others "*", to tell
// which ones are at fault; whole is the error for the spec as a whole.
func fieldErrors(spec string, whole error) []FieldError {
	fields := strings.Fields(spec)
	body := fieldsAfterTZ(spec)
	prefix := strings.Join(fields[:len(fields)-len(body)], " ")
	if len(body) != 5 && len(body) != 6 {
		return []FieldError{{Field: "expression", Value: spec, Message: whole.Error()}}
	}
	if _, err := NewParser().Parse(strings.TrimSpace(prefix + " * * * * *")); err != nil {
		return []FieldError{{Field: "expression", Value: spec, Message: err.Error()}}
	}
	names := fieldNames[6-len(body):]
	var errs []FieldError
	for i, value := range body {
		alone := slices.Repeat([]string{"*"}, len(body))
		alone[i] = value
		if _, err := NewParser().Parse(strings.TrimSpace(prefix + " " + strings.Join(alone, " "))); err != nil {
			errs = append(errs, FieldError{Field: names[i], Value: value, Message: err.Error()})
		}
	}
	if len(errs) == 0 {
		// Each field parses, together they never fire: a day of month that
		// none of the months has.
		errs = append(errs, FieldError{Field: "dayOfMonth", Value: body[len(body)-3], Message: whole.Error()})
	}
	return errs
}

// bitValues lists the values from lo to hi set in one of go-cron's field bitsets.
func bitValues(bits uint64, lo, hi int) []int {
	var out []int
	for v := lo; v <= hi; v++ {
		if bits&(1<<v) != 0 {
			out = append(out, v)
		}
	}
	return out
}
