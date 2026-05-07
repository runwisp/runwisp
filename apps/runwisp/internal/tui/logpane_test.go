// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func newTestPane(maxLines int) LogPane {
	p := NewLogPane(LogPaneConfig{
		MaxLines:    maxLines,
		LineNumbers: true,
		HScroll:     true,
	})
	p.SetSize(80, 30)
	p.SetHeaderHeight(4)
	return p
}

func TestLogPane_PrependLines_Basic(t *testing.T) {
	p := newTestPane(100_000)
	p.SetFirstLoadedLine(50)
	p.SetTotalLines(100)

	for i := 0; i < 10; i++ {
		p.AppendLine(int64(50+i), "stdout", fmt.Sprintf("line %d", 50+i))
	}
	// Scroll to the beginning
	p.scroll = 0
	p.follow = false

	oldScroll := p.scroll
	prepended := []paneLine{
		{stream: "stdout", text: "older-1"},
		{stream: "stdout", text: "older-2"},
		{stream: "stdout", text: "older-3"},
	}
	p.PrependLines(prepended, 47)

	assert.Equal(t, 13, len(p.lines))
	assert.Equal(t, "older-1", p.lines[0].text)
	assert.Equal(t, "line 50", p.lines[3].text)
	assert.Equal(t, 47, p.firstLoadedLine)
	assert.Equal(t, oldScroll+3, p.scroll, "scroll should shift by prepend count")
}

func TestLogPane_PrependLines_Empty(t *testing.T) {
	p := newTestPane(100_000)
	p.SetFirstLoadedLine(10)
	p.AppendLine(10, "stdout", "first")
	p.scroll = 0

	p.PrependLines(nil, 5)
	assert.Equal(t, 1, len(p.lines))
	assert.Equal(t, 10, p.firstLoadedLine)
}

func TestLogPane_NeedsOlder(t *testing.T) {
	p := newTestPane(100_000)

	// No older lines available — firstLoadedLine is 0
	p.SetFirstLoadedLine(0)
	p.AppendLine(0, "stdout", "first")
	p.scroll = 0
	assert.False(t, p.NeedsOlder())

	// Older lines available, but not scrolled to top
	p.SetFirstLoadedLine(50)
	p.scroll = 5
	assert.False(t, p.NeedsOlder())

	// At top with older lines available
	p.scroll = 0
	assert.True(t, p.NeedsOlder())
}

func TestLogPane_EvictAndFollow_BumpsFirstLoadedLine(t *testing.T) {
	p := newTestPane(10) // very small max
	p.SetFirstLoadedLine(100)

	for i := 0; i < 15; i++ {
		p.AppendLine(int64(100+i), "stdout", fmt.Sprintf("line %d", 100+i))
	}

	// After eviction, only 10 lines should remain
	assert.Equal(t, 10, len(p.lines))
	// firstLoadedLine should have been bumped by the excess (5)
	assert.Equal(t, 105, p.firstLoadedLine)
}

func TestLogPane_AbsoluteLineNumber(t *testing.T) {
	p := newTestPane(100_000)
	p.SetFirstLoadedLine(500)
	p.AppendLine(500, "stdout", "a")
	p.AppendLine(501, "stdout", "b")
	p.AppendLine(502, "stdout", "c")

	assert.Equal(t, 501, p.absoluteLineNumber(0))
	assert.Equal(t, 502, p.absoluteLineNumber(1))
	assert.Equal(t, 503, p.absoluteLineNumber(2))
}

func TestLogPane_PrependLines_ScrollPositionInvariance(t *testing.T) {
	p := newTestPane(100_000)
	p.SetFirstLoadedLine(100)
	p.SetTotalLines(500)

	for i := 0; i < 20; i++ {
		p.AppendLine(int64(100+i), "stdout", fmt.Sprintf("line %d", 100+i))
	}

	// User scrolls to line index 5 (absolute line 105)
	p.follow = false
	p.scroll = 5
	absLineBefore := p.absoluteLineNumber(p.scroll)

	pre := []paneLine{
		{stream: "stdout", text: "old-1"},
		{stream: "stdout", text: "old-2"},
		{stream: "stdout", text: "old-3"},
		{stream: "stdout", text: "old-4"},
		{stream: "stdout", text: "old-5"},
	}
	p.PrependLines(pre, 95)

	// After prepend, the user should still be viewing the same absolute line
	absLineAfter := p.absoluteLineNumber(p.scroll)
	assert.Equal(t, absLineBefore, absLineAfter, "scroll position should track the same absolute line after prepend")
}

func TestLogPane_EvictBelow(t *testing.T) {
	p := newTestPane(100_000)
	p.SetFirstLoadedLine(0)
	for i := 0; i < 10; i++ {
		p.AppendLine(int64(i), "stdout", fmt.Sprintf("line %d", i))
	}
	p.EvictBelow(3)
	assert.Equal(t, 7, len(p.lines))
	assert.Equal(t, 3, p.firstLoadedLine)
	assert.Equal(t, "line 3", p.lines[0].text)
}
