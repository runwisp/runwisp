// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package uikit

import (
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
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

	// nil StartAt → dash
	assert.Equal(t, "—", FormatDuration(model.Run{}))

	// < 1s
	start := now.Add(-500 * time.Millisecond)
	r := model.Run{StartAt: &start}
	d := FormatDuration(r)
	assert.Contains(t, d, "ms")

	// < 1m
	start2 := now.Add(-30 * time.Second)
	r2 := model.Run{StartAt: &start2}
	d2 := FormatDuration(r2)
	assert.Contains(t, d2, "s")
	assert.NotContains(t, d2, "m")

	// >= 1m < 1h
	start3 := now.Add(-5*time.Minute - 30*time.Second)
	r3 := model.Run{StartAt: &start3}
	d3 := FormatDuration(r3)
	assert.Contains(t, d3, "m")
	assert.Contains(t, d3, "s")

	// >= 1h
	start4 := now.Add(-90 * time.Minute)
	r4 := model.Run{StartAt: &start4}
	d4 := FormatDuration(r4)
	assert.Contains(t, d4, "h")
	assert.Contains(t, d4, "m")

	// with EndAt set
	end := now.Add(-1 * time.Second)
	start5 := now.Add(-5 * time.Second)
	r5 := model.Run{StartAt: &start5, EndAt: &end}
	d5 := FormatDuration(r5)
	assert.Contains(t, d5, "s")
}

func TestFormatTimeAgo(t *testing.T) {
	now := time.Now()

	assert.Equal(t, "just now", FormatTimeAgo(now))
	assert.Contains(t, FormatTimeAgo(now.Add(-45*time.Second)), "s ago")
	assert.Contains(t, FormatTimeAgo(now.Add(-30*time.Minute)), "m ago")
	assert.Contains(t, FormatTimeAgo(now.Add(-3*time.Hour)), "h ago")
	// Older than 24h → formatted date
	old := FormatTimeAgo(now.Add(-48 * time.Hour))
	assert.False(t, strings.Contains(old, "ago"), "expected absolute date for 48h, got %q", old)
}

func TestStatusStyle(t *testing.T) {
	statuses := []string{"running", "success", "failed", "pending", "stopped", "timeout", "crashed", "unknown-xyz"}
	for _, s := range statuses {
		style := StatusStyle(s)
		// Just verify it doesn't panic and returns a valid style
		rendered := style.Render(s)
		assert.NotEmpty(t, rendered)
	}
}

func TestPadLine(t *testing.T) {
	result := PadLine("hello", 10, ColorBg)
	assert.GreaterOrEqual(t, len([]rune(result)), 5)

	// Already at width — no padding added
	short := PadLine("hello", 3, ColorBg)
	assert.Equal(t, "hello", short)
}
