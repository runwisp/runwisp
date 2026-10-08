// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package uikit

import (
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

func TestPage_String(t *testing.T) {
	assert.Equal(t, "Home", PageHome.String())
	assert.Equal(t, "Info", PageInfo.String())
	assert.Equal(t, "Debug", PageDebug.String())
	assert.Equal(t, "Unknown", Page(99).String())
}

func TestFormatDuration(t *testing.T) {
	now := time.Now()

	// nil StartedAt → dash
	assert.Equal(t, "—", FormatDuration(model.Run{}))

	// < 1s
	start := now.Add(-500 * time.Millisecond)
	r := model.Run{StartedAt: &start}
	d := FormatDuration(r)
	assert.Contains(t, d, "ms")

	// < 1m
	start2 := now.Add(-30 * time.Second)
	r2 := model.Run{StartedAt: &start2}
	d2 := FormatDuration(r2)
	assert.Contains(t, d2, "s")
	assert.NotContains(t, d2, "m")

	// >= 1m < 1h
	start3 := now.Add(-5*time.Minute - 30*time.Second)
	r3 := model.Run{StartedAt: &start3}
	d3 := FormatDuration(r3)
	assert.Contains(t, d3, "m")
	assert.Contains(t, d3, "s")

	// >= 1h
	start4 := now.Add(-90 * time.Minute)
	r4 := model.Run{StartedAt: &start4}
	d4 := FormatDuration(r4)
	assert.Contains(t, d4, "h")
	assert.Contains(t, d4, "m")

	// with EndedAt set
	end := now.Add(-1 * time.Second)
	start5 := now.Add(-5 * time.Second)
	r5 := model.Run{StartedAt: &start5, EndedAt: &end}
	d5 := FormatDuration(r5)
	assert.Contains(t, d5, "s")
}

func TestFormatTimeAgo(t *testing.T) {
	now := time.Now()

	assert.Equal(t, "just now", FormatTimeAgo(now, nil))
	assert.Contains(t, FormatTimeAgo(now.Add(-45*time.Second), nil), "s ago")
	assert.Contains(t, FormatTimeAgo(now.Add(-30*time.Minute), nil), "m ago")
	assert.Contains(t, FormatTimeAgo(now.Add(-3*time.Hour), nil), "h ago")
	// Older than 24h → formatted date
	old := FormatTimeAgo(now.Add(-48*time.Hour), nil)
	assert.False(t, strings.Contains(old, "ago"), "expected absolute date for 48h, got %q", old)
}

func TestFormatTimestamp_UsesGivenZone(t *testing.T) {
	at := time.Date(2026, 10, 4, 1, 15, 0, 0, time.UTC)
	cest := time.FixedZone("CEST", 2*3600)

	assert.Equal(t, "2026-10-04 03:15:00", FormatTimestamp(at, cest))
	assert.Equal(t, "2026-10-04 01:15:00", FormatTimestamp(at, time.UTC))
}

func TestFormatTimeAgo_AbsoluteInGivenZone(t *testing.T) {
	cest := time.FixedZone("CEST", 2*3600)
	at := time.Now().Add(-48 * time.Hour)
	assert.Equal(t, at.In(cest).Format("Jan 02 15:04"), FormatTimeAgo(at, cest))
}

func TestResolveLocation(t *testing.T) {
	assert.Equal(t, "Europe/Bratislava", ResolveLocation("Europe/Bratislava").String())
	assert.Equal(t, time.Local, ResolveLocation(""))
	assert.Equal(t, time.Local, ResolveLocation("Not/AZone"))
}

func TestStatusStyle(t *testing.T) {
	statuses := []string{"running", "succeeded", "failed", "pending", "stopped", "timeout", "crashed", "unknown-xyz"}
	for _, s := range statuses {
		style := StatusStyle(s)
		// Just verify it doesn't panic and returns a valid style
		rendered := style.Render(s)
		assert.NotEmpty(t, rendered)
	}
}

func TestStatusColor_FailuresTakeErrorColour(t *testing.T) {
	for _, r := range []model.EndReason{
		model.ReasonFailed, model.ReasonCrashed, model.ReasonTimeout,
		model.ReasonStartFailed, model.ReasonLogOverflow, model.ReasonUnhealthy,
	} {
		assert.Equal(t, ColorError, StatusColor(string(r)), r)
	}
	assert.Equal(t, ColorWarning, StatusColor(string(model.ReasonStopped)))
	assert.Equal(t, ColorTextMuted, StatusColor(string(model.ReasonSkipped)))
}

func TestPadLine(t *testing.T) {
	result := PadLine("hello", 10, ColorBg)
	assert.GreaterOrEqual(t, len([]rune(result)), 5)

	// Already at width — no padding added
	short := PadLine("hello", 3, ColorBg)
	assert.Equal(t, "hello", short)
}

func TestRelativeTime(t *testing.T) {
	now := time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		d    time.Duration
		want string
	}{
		{5 * time.Second, "just now"},
		{45 * time.Second, "45s ago"},
		{90 * time.Second, "1m ago"},
		{30 * time.Minute, "30m ago"},
		{2 * time.Hour, "2h ago"},
		{25 * time.Hour, "yesterday"},
		{5 * 24 * time.Hour, "5d ago"},
		{45 * 24 * time.Hour, "1mo ago"},
		{2 * 30 * 24 * time.Hour, "2mo ago"},
		{400 * 24 * time.Hour, "1y ago"},
		{3 * 365 * 24 * time.Hour, "3y ago"},
	}
	for _, tt := range tests {
		got := RelativeTime(now.Add(-tt.d), now)
		assert.Equal(t, tt.want, got, "RelativeTime with d=%v", tt.d)
	}
}
