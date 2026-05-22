// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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

func TestWalkANSISegments_OSCSequence(t *testing.T) {
	// OSC hyperlink: ESC ] 8 ; ; url BEL
	input := "\x1b]8;;https://example.com\x07text\x1b]8;;\x07"
	var plain string
	WalkANSISegments(input, func(seg string, isPlain bool) {
		if isPlain {
			plain += seg
		}
	})
	assert.Equal(t, "text", plain)
}

func TestWalkANSISegments_STSequence(t *testing.T) {
	// DCS sequence: ESC P ... ESC \  followed by plain text
	input := "\x1bPsome data\x1b\\hello"
	var plain string
	WalkANSISegments(input, func(seg string, isPlain bool) {
		if isPlain {
			plain += seg
		}
	})
	assert.Equal(t, "hello", plain)
}

func TestWalkANSISegments_TwoByte(t *testing.T) {
	// ESC M (reverse index) followed by plain text
	input := "\x1bMplain"
	var plain string
	WalkANSISegments(input, func(seg string, isPlain bool) {
		if isPlain {
			plain += seg
		}
	})
	assert.Equal(t, "plain", plain)
}

func TestPadLine(t *testing.T) {
	result := PadLine("hello", 10, ColorBg)
	assert.GreaterOrEqual(t, len([]rune(result)), 5)

	// Already at width — no padding added
	short := PadLine("hello", 3, ColorBg)
	assert.Equal(t, "hello", short)
}

func TestWalkANSISegments_OSCWithSTTerminator(t *testing.T) {
	// OSC with ST terminator: ESC ] text ESC \
	input := "\x1b]8;;https://example.com\x1b\\text"
	var plain string
	WalkANSISegments(input, func(seg string, isPlain bool) {
		if isPlain {
			plain += seg
		}
	})
	assert.Equal(t, "text", plain)
}

func TestWalkANSISegments_CSISequence(t *testing.T) {
	// CSI sequence: ESC [ params final — e.g. ESC[32m (green foreground)
	input := "\x1b[32mhello\x1b[0m"
	var plain string
	WalkANSISegments(input, func(seg string, isPlain bool) {
		if isPlain {
			plain += seg
		}
	})
	assert.Equal(t, "hello", plain)
}

func TestWalkANSISegments_DCSSequence(t *testing.T) {
	// DCS sequence: ESC P data ESC \ — ends at ST
	input := "\x1bPdata\x1b\\text"
	var plain string
	WalkANSISegments(input, func(seg string, isPlain bool) {
		if isPlain {
			plain += seg
		}
	})
	assert.Equal(t, "text", plain)
}

func TestWalkANSISegments_ESCAtEndOfString(t *testing.T) {
	// ESC at the very end of the string — no following byte
	input := "hello\x1b"
	var segments []string
	var kinds []bool
	WalkANSISegments(input, func(seg string, isPlain bool) {
		segments = append(segments, seg)
		kinds = append(kinds, isPlain)
	})
	// "hello" is printable, lone ESC is a non-printable sequence
	assert.Len(t, segments, 2)
	assert.True(t, kinds[0], "hello is printable")
	assert.False(t, kinds[1], "lone ESC is non-printable")
}
