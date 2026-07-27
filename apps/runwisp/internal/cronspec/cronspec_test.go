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
