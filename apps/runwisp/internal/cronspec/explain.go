// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cronspec

import (
	"fmt"
	"iter"
	"strings"
	"time"

	cron "github.com/netresearch/go-cron"

	lnquy "github.com/lnquy/cron"
)

// Resolve builds the cron spec actually fed to the parser (cronExpr prefixed
// with CRON_TZ= when the task pins its own timezone) and the location used as
// the reference for wall-clock evaluation: the task's own timezone when set,
// else defaultLoc, else time.Local. Shared by the live scheduler, startup
// catch-up and the config checks so all of them interpret a schedule
// identically.
func Resolve(cronExpr, taskTZ string, defaultLoc *time.Location) (spec string, loc *time.Location) {
	spec, loc = cronExpr, defaultLoc
	if loc == nil {
		loc = time.Local
	}
	if taskTZ != "" {
		// CRON_TZ= is honored by the parser and overrides the default location
		// for this entry.
		spec = "CRON_TZ=" + taskTZ + " " + cronExpr
		// Best-effort resolve: if it doesn't load, the caller's Parse fails and
		// surfaces the error. On success the task zone is the reference.
		if l, err := time.LoadLocation(taskTZ); err == nil {
			loc = l
		}
	}
	return spec, loc
}

// IsFixedInterval reports whether a schedule fires on a fixed duration
// (@every) instead of matching wall-clock fields. Such a schedule has no
// wall-clock time it can repeat: on a fall-back day every one of its firings
// in the rewound hour is a genuine tick of real elapsed time, even though an
// interval dividing an hour evenly ("@every 30m") lands each of them on a
// local time already seen an hour earlier. The wall-clock dedup must therefore
// skip them, mirroring the spring-forward exemption dstGapSchedule already
// makes. Its phase is also set by when the task is registered, not by the
// config, so its firing times cannot be previewed.
func IsFixedInterval(schedule cron.Schedule) bool {
	_, ok := schedule.(cron.ConstantDelaySchedule)
	return ok
}

// Firing is one scheduled tick as the scheduler will treat it.
type Firing struct {
	At time.Time
	// Shifted: the wall time falls in a spring-forward gap and fires at the
	// first valid instant after it (see dstGapSchedule).
	Shifted bool
	// Skipped: a fall-back repeat of a wall-clock second that already fired in
	// the same wall hour; the scheduler records it as dst_skipped instead of
	// running it. Never set for @every, which has no wall-clock time to repeat.
	Skipped bool
}

// wall is a wall-clock second, comparable across a fall-back rewind where the
// UTC offset differs.
type wall struct {
	year        int
	month       time.Month
	day, hour   int
	minute, sec int
}

// Firings yields sched's ticks after from, evaluated in loc, forever (callers
// stop the iteration). It is the read-only counterpart of the live scheduler:
// the DST rules mirror dstGapSchedule and runtime's Scheduler.fireOnce, so what
// `validate --explain` prints is what the daemon will do.
func Firings(sched cron.Schedule, loc *time.Location, from time.Time) iter.Seq[Firing] {
	return func(yield func(Firing) bool) {
		fixed := IsFixedInterval(sched)
		gap, hasGap := sched.(dstGapSchedule)
		seen := map[wall]struct{}{}
		var seenHour wall
		for t := from; ; {
			var next time.Time
			shifted := false
			if hasGap {
				next, shifted = gap.next(t.In(loc))
			} else {
				next = sched.Next(t.In(loc))
			}
			if next.IsZero() {
				return
			}
			next = next.In(loc)
			t = next

			w := wall{next.Year(), next.Month(), next.Day(), next.Hour(), next.Minute(), next.Second()}
			if hour := (wall{year: w.year, month: w.month, day: w.day, hour: w.hour}); hour != seenHour {
				seenHour, seen = hour, map[wall]struct{}{}
			}
			_, skipped := seen[w]
			seen[w] = struct{}{}
			if !yield(Firing{At: next, Shifted: shifted, Skipped: skipped && !fixed}) {
				return
			}
		}
	}
}

var descriptors = map[string]string{
	"@yearly": "0 0 1 1 *", "@annually": "0 0 1 1 *", "@monthly": "0 0 1 * *",
	"@weekly": "0 0 * * 0", "@daily": "0 0 * * *", "@midnight": "0 0 * * *", "@hourly": "0 * * * *",
}

// Describe renders a cron expression in plain English ("At 03:00"), the CLI
// twin of the web UI's humanizeCron. A leading TZ= / CRON_TZ= prefix is
// ignored, and anything it cannot describe comes back as the expression itself.
func Describe(spec string) string {
	expr := strings.Join(fieldsAfterTZ(spec), " ")
	if every, ok := strings.CutPrefix(expr, "@every "); ok {
		if words := describeEvery(strings.TrimSpace(every)); words != "" {
			return words
		}
		return expr
	}
	if five, ok := descriptors[expr]; ok {
		expr = five
	}
	// The descriptor reads a last weekday as Quartz's "5L"; "5#L" would come out
	// as "on the Friday of the month".
	expr = strings.NewReplacer("#L", "L", "#l", "l").Replace(expr)
	d, err := lnquy.NewDescriptor(lnquy.Use24HourTimeFormat(true))
	if err != nil {
		return expr
	}
	words, err := d.ToDescription(expr, lnquy.Locale_en)
	if err != nil {
		return expr
	}
	return words
}

// describeEvery turns a Go duration into "Every 1 hour 30 minutes", or "" for
// one that does not parse or is below a second.
func describeEvery(duration string) string {
	d, err := time.ParseDuration(duration)
	if err != nil || d < time.Second {
		return ""
	}
	var parts []string
	for _, u := range []struct {
		unit time.Duration
		name string
	}{{time.Hour, "hour"}, {time.Minute, "minute"}, {time.Second, "second"}} {
		n := int64(d / u.unit)
		d %= u.unit
		if n == 0 {
			continue
		}
		if n != 1 {
			u.name += "s"
		}
		parts = append(parts, fmt.Sprintf("%d %s", n, u.name))
	}
	return "Every " + strings.Join(parts, " ")
}

// FormatInstant renders t like "Mon 2026-10-19 03:00 CEST", with seconds only
// when they are not zero. The one wording for a firing time in warnings and in
// `validate --explain`.
func FormatInstant(t time.Time) string {
	layout := "Mon 2006-01-02 15:04 MST"
	if t.Second() != 0 {
		layout = "Mon 2006-01-02 15:04:05 MST"
	}
	return t.Format(layout)
}
