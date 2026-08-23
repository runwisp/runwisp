// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cronspec

import (
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		name     string
		spec     string
		timezone string
		wantErr  bool
	}{
		{name: "five fields", spec: "0 3 * * *"},
		{name: "step", spec: "*/5 * * * *"},
		{name: "descriptor hourly", spec: "@hourly"},
		{name: "descriptor every", spec: "@every 1h30m"},
		{name: "with timezone", spec: "0 3 * * *", timezone: "Europe/Bratislava"},
		{name: "six fields with seconds", spec: "0 0 3 * * *"},
		{name: "six fields sub-minute step", spec: "*/30 * * * * *"},
		{name: "six fields with timezone", spec: "*/30 * * * * *", timezone: "Europe/Bratislava"},
		{name: "day-of-week 7 is sunday", spec: "47 6 * * 7"},
		{name: "day-of-week list with 7", spec: "0 3 * * 1,7"},
		{name: "day-of-week range ending in 7", spec: "0 3 * * 5-7"},
		{name: "six fields with day-of-week 7", spec: "0 47 6 * * 7"},
		{name: "day-of-week 7 with a timezone", spec: "47 6 * * 7", timezone: "Europe/Bratislava"},
		{name: "four fields", spec: "* * * *", wantErr: true},
		{name: "seven fields", spec: "0 0 0 3 * * *", wantErr: true},
		{name: "out of range minute", spec: "61 * * * *", wantErr: true},
		{name: "out of range second", spec: "61 * * * * *", wantErr: true},
		{name: "garbage", spec: "every day at noon", wantErr: true},
		{name: "bad timezone", spec: "0 3 * * *", timezone: "Mars/Olympus", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.spec, tt.timezone)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestSundayIsSeven is the regression test for the day-of-week 7 rejection.
// robfig/cron bounds dow at 0-6, so `47 6 * * 7` — the line Debian's and
// Ubuntu's own /etc/crontab ships to run /etc/cron.weekly — was refused with
// "end of range (7) above maximum (6)" and the box's housekeeping silently
// stopped being scheduled. Every 7-form must now parse and fire exactly like the
// 0-form it means.
func TestSundayIsSeven(t *testing.T) {
	// tuesday is an ordinary evaluation point with no DST transition near it.
	tuesday := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		spec  string
		equiv string
	}{
		{name: "bare 7", spec: "47 6 * * 7", equiv: "47 6 * * 0"},
		{name: "list containing 7", spec: "0 3 * * 1,7", equiv: "0 3 * * 0,1"},
		{name: "range ending in 7", spec: "0 3 * * 5-7", equiv: "0 3 * * 0,5,6"},
		{name: "range starting at 7", spec: "0 3 * * 7-1", equiv: "0 3 * * 0-1"},
		// vixie folds day 7 into day 0 after expanding the range, so 1-7 is the
		// whole week — not an inverted range, and not Monday alone.
		{name: "range 1-7 is every day", spec: "0 3 * * 1-7", equiv: "0 3 * * *"},
		{name: "range 0-7 is every day", spec: "0 3 * * 0-7", equiv: "0 3 * * *"},
		{name: "range with a step", spec: "0 3 * * 5-7/2", equiv: "0 3 * * 0,5"},
		// A bare 7 is already vixie's field max, so no step can ever add a value
		// beyond Sunday itself — this must resolve to Sunday only, not wrap around
		// into other days under robfig's 0-6 bounds.
		{name: "bare 7 with a step", spec: "0 3 * * 7/2", equiv: "0 3 * * 0"},
		{name: "bare 7 with a step of 1", spec: "0 3 * * 7/1", equiv: "0 3 * * 0"},
		{name: "list with a stepped 7", spec: "0 3 * * 1,7/3", equiv: "0 3 * * 0,1"},
		{name: "six-field form", spec: "30 47 6 * * 7", equiv: "30 47 6 * * 0"},
		{name: "with a CRON_TZ prefix", spec: "CRON_TZ=UTC 47 6 * * 7", equiv: "CRON_TZ=UTC 47 6 * * 0"},
		// The rewrite must not reach any other field, nor a step of 7.
		{name: "step of 7 in dow untouched", spec: "0 3 * * */7", equiv: "0 3 * * 0"},
		{name: "step of 7 in minutes untouched", spec: "*/7 * * * *", equiv: "*/7 * * * *"},
		{name: "7 in the other fields untouched", spec: "7 7 7 7 *", equiv: "7 7 7 7 *"},
		{name: "named sunday still works", spec: "0 3 * * SUN", equiv: "0 3 * * 0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sched, err := NewScheduleParser().Parse(tt.spec)
			require.NoError(t, err)
			want, err := NewScheduleParser().Parse(tt.equiv)
			require.NoError(t, err)

			from := tuesday
			for i := 0; i < 10; i++ {
				got := sched.Next(from)
				assert.Equal(t, want.Next(from), got, "firing %d from %s", i, from)
				from = got
			}
		})
	}
}

// TestStockDebianWeeklyLineFiresOnSunday pins the actual behaviour the alias
// exists for, rather than only an equivalence between two specs.
func TestStockDebianWeeklyLineFiresOnSunday(t *testing.T) {
	sched, err := NewScheduleParser().Parse("47 6 * * 7")
	require.NoError(t, err)

	next := sched.Next(time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC))
	assert.Equal(t, time.Sunday, next.Weekday())
	assert.Equal(t, time.Date(2026, 8, 9, 6, 47, 0, 0, time.UTC), next)
}

// TestSupersetOfStandardParser guards the contract that cronspec's grammar is a
// superset of robfig's standard parser — the one cron.New would use without an
// explicit WithParser. Every spec the standard parser accepts, cronspec must
// also accept (no 5-field regression); cronspec additionally accepts a leading
// seconds field. If ParseOptions ever dropped a standard option, validation and
// scheduling would reject specs the userbase already relies on.
func TestSupersetOfStandardParser(t *testing.T) {
	specs := []string{
		"0 3 * * *",
		"*/5 * * * *",
		"@hourly",
		"@daily",
		"@every 90m",
		"CRON_TZ=UTC 0 3 * * *",
		"* * * *",
		"61 * * * *",
		"nonsense",
	}
	for _, spec := range specs {
		_, oursErr := NewParser().Parse(spec)
		_, stdErr := cron.ParseStandard(spec)
		if stdErr == nil {
			require.NoError(t, oursErr,
				"cronspec rejected a spec the standard parser accepts (5-field regression): %q", spec)
		}
	}
}

// TestAcceptsSixFieldSeconds is the additive half of the superset contract: the
// leading seconds field that the standard parser rejects, cronspec accepts.
func TestAcceptsSixFieldSeconds(t *testing.T) {
	spec := "*/30 * * * * *"

	_, oursErr := NewParser().Parse(spec)
	require.NoError(t, oursErr, "cronspec should accept a 6-field spec")

	_, stdErr := cron.ParseStandard(spec)
	require.Error(t, stdErr, "standard parser should still reject a 6-field spec")
}

// TestSpringForwardGapRecovered is the regression test for the spring-forward
// DST skip: a daily tick whose wall-clock time falls in the missing hour must
// fire at the gap end ("the next valid time, once"), not silently vanish and
// wrap to the next day. Europe/Bratislava springs forward on 2024-03-31, when
// 02:00 → 03:00 and the whole 02:00 hour never occurs locally.
func TestSpringForwardGapRecovered(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Bratislava")
	require.NoError(t, err)

	parser := NewScheduleParser()
	// Evaluate from just after midnight on the spring-forward day.
	from := time.Date(2024, 3, 31, 0, 30, 0, 0, loc)

	tests := []struct {
		name string
		spec string
		// want is the expected local wall-clock of the next firing, in loc.
		want time.Time
	}{
		{
			// The bug: naive Next steps 01:xx straight to 03:00, never matches
			// hour 2, and wraps to 2024-04-01 02:00. We must fire at the gap end.
			name: "tick in the gap fires at gap end",
			spec: "0 2 * * *",
			want: time.Date(2024, 3, 31, 3, 0, 0, 0, loc),
		},
		{
			// :30 inside the missing hour normalizes forward by the gap (one
			// hour), exactly as time.Date would for the non-existent 02:30.
			name: "sub-hour tick in the gap normalizes forward",
			spec: "30 2 * * *",
			want: time.Date(2024, 3, 31, 3, 30, 0, 0, loc),
		},
		{
			// A tick after the gap is untouched — the recovery must not move it.
			name: "tick after the gap is untouched",
			spec: "0 5 * * *",
			want: time.Date(2024, 3, 31, 5, 0, 0, 0, loc),
		},
		{
			// The gap-end time itself exists and is matched normally.
			name: "tick at the gap end is untouched",
			spec: "0 3 * * *",
			want: time.Date(2024, 3, 31, 3, 0, 0, 0, loc),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sched, err := parser.Parse(tt.spec)
			require.NoError(t, err)
			got := sched.Next(from)
			assert.True(t, tt.want.Equal(got),
				"want %s, got %s", tt.want.Format("2006-01-02 15:04:05 MST"), got.In(loc).Format("2006-01-02 15:04:05 MST"))
		})
	}
}

// TestSpringForwardFiresOnce confirms the recovered gap tick is a single real
// instant: stepping Next forward from it yields the following day's tick, not a
// repeat of the same firing.
func TestSpringForwardFiresOnce(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Bratislava")
	require.NoError(t, err)

	sched, err := NewScheduleParser().Parse("0 2 * * *")
	require.NoError(t, err)

	first := sched.Next(time.Date(2024, 3, 31, 0, 30, 0, 0, loc))
	require.True(t, time.Date(2024, 3, 31, 3, 0, 0, 0, loc).Equal(first))

	next := sched.Next(first)
	want := time.Date(2024, 4, 1, 2, 0, 0, 0, loc)
	assert.True(t, want.Equal(next), "want %s, got %s", want, next.In(loc))
}

// TestScheduleParser_RejectsInvalidSpec confirms the DST-recovering parser
// surfaces grammar errors from the underlying parser unchanged.
func TestScheduleParser_RejectsInvalidSpec(t *testing.T) {
	_, err := NewScheduleParser().Parse("every day at noon")
	assert.Error(t, err)
}

// TestScheduleParser_EveryPassesThrough confirms an @every schedule
// (ConstantDelaySchedule, not a wall-clock SpecSchedule) bypasses the DST-gap
// wrapper and still advances by its fixed duration.
func TestScheduleParser_EveryPassesThrough(t *testing.T) {
	sched, err := NewScheduleParser().Parse("@every 1h")
	require.NoError(t, err)

	from := time.Date(2024, 6, 15, 0, 30, 0, 0, time.UTC)
	got := sched.Next(from)
	assert.True(t, from.Add(time.Hour).Equal(got), "want %s, got %s", from.Add(time.Hour), got)
}

// TestScheduleParser_NeverMatchingSpecReturnsZero covers the IsZero short-circuit:
// "Feb 30" never occurs, so the underlying Next returns the zero time and the
// wrapper hands it straight back without DST math.
func TestScheduleParser_NeverMatchingSpecReturnsZero(t *testing.T) {
	sched, err := NewScheduleParser().Parse("0 0 30 2 *")
	require.NoError(t, err)

	got := sched.Next(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	assert.True(t, got.IsZero(), "a spec that can never fire must yield the zero time")
}

// TestNonDSTDayUnaffected guards that the wrapper is a no-op on an ordinary day
// with no transition between the evaluation point and the next firing.
func TestNonDSTDayUnaffected(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Bratislava")
	require.NoError(t, err)

	sched, err := NewScheduleParser().Parse("0 2 * * *")
	require.NoError(t, err)

	from := time.Date(2024, 6, 15, 0, 30, 0, 0, loc)
	got := sched.Next(from)
	want := time.Date(2024, 6, 15, 2, 0, 0, 0, loc)
	assert.True(t, want.Equal(got), "want %s, got %s", want, got.In(loc))
}
