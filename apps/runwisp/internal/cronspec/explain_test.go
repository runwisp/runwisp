// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cronspec

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func firings(t *testing.T, spec, zone, from string, n int) []Firing {
	t.Helper()
	loc, err := time.LoadLocation(zone)
	require.NoError(t, err)
	sched, err := NewParser().Parse(spec)
	require.NoError(t, err)
	start, err := time.ParseInLocation("2006-01-02 15:04", from, loc)
	require.NoError(t, err)
	var out []Firing
	for f := range Firings(sched, loc, start) {
		out = append(out, f)
		if len(out) == n {
			break
		}
	}
	return out
}

func TestFiringsMarksSpringForwardShift(t *testing.T) {
	// 2027-03-28 02:30 does not exist in Europe/Bratislava; the run lands at
	// 03:30, the instant time.Date normalizes the missing wall time to.
	got := firings(t, "30 2 * * *", "Europe/Bratislava", "2027-03-27 12:00", 3)
	require.Len(t, got, 3)
	require.True(t, got[0].Shifted)
	require.Equal(t, "03:30", got[0].At.Format("15:04"))
	require.False(t, got[1].Shifted)
	require.False(t, got[2].Shifted)

	// A midnight gap go-cron does not recover on its own.
	got = firings(t, "0 0 * * *", "America/Santiago", "2026-09-05 12:00", 2)
	require.True(t, got[0].Shifted)
	require.False(t, got[1].Shifted)
}

func TestFiringsMarksFallBackRepeat(t *testing.T) {
	// 02:30 happens twice on 2026-10-25 in Europe/Bratislava; the scheduler
	// drops the second pass as dst_skipped.
	got := firings(t, "30 2 * * *", "Europe/Bratislava", "2026-10-24 12:00", 3)
	require.Len(t, got, 3)
	require.False(t, got[0].Skipped)
	require.True(t, got[1].Skipped)
	require.False(t, got[2].Skipped)
	require.Equal(t, "2026-10-26", got[2].At.Format("2006-01-02"))
}

func TestFiringsFixedIntervalIsNeverSkipped(t *testing.T) {
	for _, f := range firings(t, "@every 30m", "Europe/Bratislava", "2026-10-25 00:00", 8) {
		require.False(t, f.Skipped)
	}
}

func TestFiringsFallBackRepeatOfHourlyWallTime(t *testing.T) {
	// "0,30 * * * *" ticks at 02:00 and 02:30 twice; the second pass is skipped.
	got := firings(t, "0,30 * * * *", "Europe/Bratislava", "2026-10-25 01:00", 6)
	var skipped int
	for _, f := range got {
		if f.Skipped {
			skipped++
		}
	}
	require.Equal(t, 2, skipped, "%v", got)
}

func TestDescribe(t *testing.T) {
	for spec, want := range map[string]string{
		"0 3 * * *":                      "At 03:00",
		"*/5 * * * *":                    "Every 5 minutes",
		"CRON_TZ=Europe/Paris 0 3 * * *": "At 03:00",
		"@every 1h30m":                   "Every 1 hour 30 minutes",
		"@every 90s":                     "Every 1 minute 30 seconds",
		"@every 500ms":                   "@every 500ms",
		"@daily":                         "At 00:00",
		"0 0 * * FRI#3":                  "At 00:00, on the third Friday of the month",
		"0 0 * * FRI#L":                  "At 00:00, on the last Friday of the month",
		"0 0 * * 7#L":                    "At 00:00, on the last Sunday of the month",
		"not a cron":                     "not a cron",
	} {
		require.Equal(t, want, Describe(spec), spec)
	}
}

func TestResolve(t *testing.T) {
	bratislava, err := time.LoadLocation("Europe/Bratislava")
	require.NoError(t, err)
	spec, loc := Resolve("0 3 * * *", "", bratislava)
	require.Equal(t, "0 3 * * *", spec)
	require.Equal(t, bratislava, loc)
	spec, loc = Resolve("0 3 * * *", "Asia/Tokyo", bratislava)
	require.Equal(t, "CRON_TZ=Asia/Tokyo 0 3 * * *", spec)
	require.Equal(t, "Asia/Tokyo", loc.String())
	_, loc = Resolve("0 3 * * *", "", nil)
	require.Equal(t, time.Local, loc)
}

// TestFiringsMarksGapAfterLongSpan: the last Sunday of March 2030 is the
// spring-forward day, and the previous tick is a year earlier in the same UTC
// offset, so only the tick itself shows the shift.
func TestFiringsMarksGapAfterLongSpan(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Bratislava")
	require.NoError(t, err)
	sched, err := NewParser().Parse("30 2 L 3 *")
	require.NoError(t, err)
	for f := range Firings(sched, loc, time.Date(2029, 4, 1, 0, 0, 0, 0, loc)) {
		require.Equal(t, time.Date(2030, 3, 31, 3, 30, 0, 0, loc), f.At)
		require.True(t, f.Shifted)
		return
	}
}
