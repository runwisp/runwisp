// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package logpane

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/assert"
)

func newTestPane(maxLines int) Pane {
	p := NewPane(Config{
		MaxLines:    maxLines,
		LineNumbers: true,
		HScroll:     true,
	})
	p.SetSize(80, 30)
	p.SetHeaderHeight(4)
	return p
}

func TestLogPane_RegionOverlay_RendersBelowCommitted(t *testing.T) {
	p := newTestPane(100_000)
	p.AppendLine(0, "stdout", "committed line")
	p.SetRegion("stdout", 1, []string{"progress 42%"})

	var b strings.Builder
	p.RenderLines(&b, false, false)
	out := b.String()
	assert.Contains(t, out, "committed line")
	assert.Contains(t, out, "progress 42%")

	// A newer frame replaces the previous one wholesale.
	p.SetRegion("stdout", 1, []string{"progress 99%"})
	b.Reset()
	p.RenderLines(&b, false, false)
	out = b.String()
	assert.Contains(t, out, "progress 99%")
	assert.NotContains(t, out, "progress 42%")
}

func TestLogPane_RegionOverlay_EmptyRowsClears(t *testing.T) {
	p := newTestPane(100_000)
	p.AppendLine(0, "stdout", "committed line")
	p.SetRegion("stdout", 1, []string{"live row"})
	assert.Equal(t, 2, p.renderableLen())

	p.SetRegion("stdout", 1, nil)
	assert.Equal(t, 1, p.renderableLen())

	p.SetRegion("stdout", 1, []string{"again"})
	p.ClearRegions()
	assert.Equal(t, 1, p.renderableLen())
}

func TestLogPane_RegionOverlay_StdoutBeforeStderr(t *testing.T) {
	p := newTestPane(100_000)
	p.SetRegion("stderr", 1, []string{"err frame"})
	p.SetRegion("stdout", 1, []string{"out frame"})

	lines := p.overlayLines()
	assert.Equal(t, []Line{{Stream: "stdout", Text: "out frame"}, {Stream: "stderr", Text: "err frame"}}, lines)
}

func TestLogPane_PrependLines_Basic(t *testing.T) {
	p := newTestPane(100_000)
	p.SetFirstLoadedLine(50)
	p.SetTotalLines(100)

	for i := 0; i < 10; i++ {
		p.AppendLine(int64(50+i), "stdout", fmt.Sprintf("line %d", 50+i))
	}
	// Scroll to the beginning
	p.Scroll = 0
	p.Follow = false

	oldScroll := p.Scroll
	prepended := []Line{
		{Stream: "stdout", Text: "older-1"},
		{Stream: "stdout", Text: "older-2"},
		{Stream: "stdout", Text: "older-3"},
	}
	p.PrependLines(prepended, 47)

	assert.Equal(t, 13, len(p.Lines))
	assert.Equal(t, "older-1", p.Lines[0].Text)
	assert.Equal(t, "line 50", p.Lines[3].Text)
	assert.Equal(t, 47, p.FirstLoadedLine)
	assert.Equal(t, oldScroll+3, p.Scroll, "scroll should shift by prepend count")
}

func TestLogPane_PrependLines_Empty(t *testing.T) {
	p := newTestPane(100_000)
	p.SetFirstLoadedLine(10)
	p.AppendLine(10, "stdout", "first")
	p.Scroll = 0

	p.PrependLines(nil, 5)
	assert.Equal(t, 1, len(p.Lines))
	assert.Equal(t, 10, p.FirstLoadedLine)
}

func TestLogPane_NeedsOlder(t *testing.T) {
	p := newTestPane(100_000)

	// No older lines available — firstLoadedLine is 0
	p.SetFirstLoadedLine(0)
	p.AppendLine(0, "stdout", "first")
	p.Scroll = 0
	assert.False(t, p.NeedsOlder())

	// Older lines available, but not scrolled to top
	p.SetFirstLoadedLine(50)
	p.Scroll = 5
	assert.False(t, p.NeedsOlder())

	// At top with older lines available
	p.Scroll = 0
	assert.True(t, p.NeedsOlder())
}

func TestLogPane_EvictAndFollow_BumpsFirstLoadedLine(t *testing.T) {
	p := newTestPane(10) // very small max
	p.SetFirstLoadedLine(100)

	for i := 0; i < 15; i++ {
		p.AppendLine(int64(100+i), "stdout", fmt.Sprintf("line %d", 100+i))
	}

	// After eviction, only 10 lines should remain
	assert.Equal(t, 10, len(p.Lines))
	// firstLoadedLine should have been bumped by the excess (5)
	assert.Equal(t, 105, p.FirstLoadedLine)
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
	p.Follow = false
	p.Scroll = 5
	absLineBefore := p.absoluteLineNumber(p.Scroll)

	pre := []Line{
		{Stream: "stdout", Text: "old-1"},
		{Stream: "stdout", Text: "old-2"},
		{Stream: "stdout", Text: "old-3"},
		{Stream: "stdout", Text: "old-4"},
		{Stream: "stdout", Text: "old-5"},
	}
	p.PrependLines(pre, 95)

	// After prepend, the user should still be viewing the same absolute line
	absLineAfter := p.absoluteLineNumber(p.Scroll)
	assert.Equal(t, absLineBefore, absLineAfter, "scroll position should track the same absolute line after prepend")
}

func TestLogPane_EvictBelow(t *testing.T) {
	p := newTestPane(100_000)
	p.SetFirstLoadedLine(0)
	for i := 0; i < 10; i++ {
		p.AppendLine(int64(i), "stdout", fmt.Sprintf("line %d", i))
	}
	p.EvictBelow(3)
	assert.Equal(t, 7, len(p.Lines))
	assert.Equal(t, 3, p.FirstLoadedLine)
	assert.Equal(t, "line 3", p.Lines[0].Text)
}

func TestLogPane_ScrollUp(t *testing.T) {
	p := newTestPane(100_000)
	for i := 0; i < 50; i++ {
		p.AppendLine(int64(i), "stdout", fmt.Sprintf("line %d", i))
	}
	p.Scroll = 20
	p.Follow = false

	p.ScrollUp(5)
	assert.Equal(t, 15, p.Scroll)
	assert.False(t, p.Follow)

	// Clamp at 0
	p.ScrollUp(100)
	assert.Equal(t, 0, p.Scroll)
}

func TestLogPane_ScrollUp_AtZeroNoOp(t *testing.T) {
	p := newTestPane(100_000)
	p.AppendLine(0, "stdout", "line")
	p.Scroll = 0

	p.ScrollUp(3)
	assert.Equal(t, 0, p.Scroll)
}

func TestLogPane_ScrollDown(t *testing.T) {
	p := newTestPane(100_000)
	for i := 0; i < 50; i++ {
		p.AppendLine(int64(i), "stdout", fmt.Sprintf("line %d", i))
	}
	p.Scroll = 0
	p.Follow = false

	p.ScrollDown(5)
	assert.Equal(t, 5, p.Scroll)
}

func TestLogPane_ScrollDown_TriggersFollow(t *testing.T) {
	p := newTestPane(100_000)
	for i := 0; i < 10; i++ {
		p.AppendLine(int64(i), "stdout", fmt.Sprintf("line %d", i))
	}
	p.Follow = false
	p.Scroll = p.maxScroll() - 1

	p.ScrollDown(100)
	assert.True(t, p.Follow)
	assert.Equal(t, p.maxScroll(), p.Scroll)
}

func TestLogPane_FirstLoadedLineNum(t *testing.T) {
	p := newTestPane(100_000)
	p.SetFirstLoadedLine(42)
	assert.Equal(t, 42, p.FirstLoadedLineNum())
}

func TestLogPane_HandleKeyScroll_HScroll(t *testing.T) {
	p := newTestPane(100_000)
	p.Cfg.HScroll = true
	// Add a long line so MaxHScroll > 0
	p.AppendLine(0, "stdout", fmt.Sprintf("%s", fmt.Sprintf("%-200s", "x")))
	p.Scroll = 0
	p.Follow = false

	// scroll right
	consumed := p.HandleKeyScroll("right")
	assert.True(t, consumed)
	assert.Equal(t, HScrollStep, p.HScroll)

	// scroll left
	consumed = p.HandleKeyScroll("left")
	assert.True(t, consumed)
	assert.Equal(t, 0, p.HScroll)

	// shift+right jumps further
	p.HScroll = 0
	consumed = p.HandleKeyScroll("shift+right")
	assert.True(t, consumed)
	assert.Greater(t, p.HScroll, 0)

	// shift+left resets to 0
	consumed = p.HandleKeyScroll("shift+left")
	assert.True(t, consumed)
	assert.Equal(t, 0, p.HScroll)
}

func TestLogPane_HandleKeyScroll_VerticalKeys(t *testing.T) {
	p := newTestPane(100_000)
	for i := 0; i < 100; i++ {
		p.AppendLine(int64(i), "stdout", fmt.Sprintf("line %d", i))
	}
	p.Scroll = 10
	p.Follow = false

	assert.True(t, p.HandleKeyScroll("up"))
	assert.Equal(t, 9, p.Scroll)

	assert.True(t, p.HandleKeyScroll("down"))
	assert.Equal(t, 10, p.Scroll)

	assert.True(t, p.HandleKeyScroll("pgup"))
	assert.Less(t, p.Scroll, 10)

	p.Scroll = 0
	p.Follow = false
	assert.True(t, p.HandleKeyScroll("pgdown"))
	assert.Greater(t, p.Scroll, 0)

	assert.True(t, p.HandleKeyScroll("g"))
	assert.Equal(t, 0, p.Scroll)

	assert.True(t, p.HandleKeyScroll("G"))
	assert.Equal(t, p.maxScroll(), p.Scroll)

	// Unknown key → false
	assert.False(t, p.HandleKeyScroll("z"))
}

func TestLogPane_ScrollLeft_ClampedAtZero(t *testing.T) {
	p := newTestPane(100_000)
	p.HScroll = 0

	p.scrollLeft()
	assert.Equal(t, 0, p.HScroll)

	p.HScroll = 4
	p.scrollLeft()
	assert.Equal(t, 0, p.HScroll) // 4 - 8 clamped to 0
}

// ---- SetLineNumbers ----

func TestLogPane_SetLineNumbers_EnableDisable(t *testing.T) {
	p := newTestPane(100_000)
	assert.True(t, p.Cfg.LineNumbers)

	p.SetLineNumbers(false)
	assert.False(t, p.Cfg.LineNumbers)

	p.SetLineNumbers(true)
	assert.True(t, p.Cfg.LineNumbers)
}

// ---- MaxScroll ----

func TestLogPane_MaxScroll_ExposedMethod(t *testing.T) {
	p := newTestPane(100_000)
	for i := 0; i < 50; i++ {
		p.AppendLine(int64(i), "stdout", fmt.Sprintf("line %d", i))
	}
	// MaxScroll (exported) must equal maxScroll (internal).
	assert.Equal(t, p.maxScroll(), p.MaxScroll())
}

func TestLogPane_MaxScroll_Empty(t *testing.T) {
	p := newTestPane(100_000)
	assert.Equal(t, 0, p.MaxScroll())
}

// ---- scrollRight ----

func TestLogPane_ScrollRight_AdvancesHScroll(t *testing.T) {
	p := newTestPane(100_000)
	p.AppendLine(0, "stdout", fmt.Sprintf("%-200s", "x"))
	p.Scroll = 0
	p.Follow = false

	p.scrollRight()
	assert.Equal(t, HScrollStep, p.HScroll)
}

func TestLogPane_ScrollRight_ClampsAtMax(t *testing.T) {
	p := newTestPane(100_000)
	p.AppendLine(0, "stdout", fmt.Sprintf("%-200s", "x"))
	p.Scroll = 0
	p.Follow = false
	maxH := p.MaxHScroll()
	p.HScroll = maxH // already at max
	p.scrollRight()
	assert.Equal(t, maxH, p.HScroll)
}

// ---- scrollPageDown ----

func TestLogPane_ScrollPageDown_AdvancesScroll(t *testing.T) {
	p := newTestPane(100_000)
	for i := 0; i < 100; i++ {
		p.AppendLine(int64(i), "stdout", fmt.Sprintf("line %d", i))
	}
	p.Scroll = 0
	p.Follow = false
	vis := p.VisibleLines()
	ms := p.maxScroll()

	p.scrollPageDown(ms)
	assert.Equal(t, vis, p.Scroll)
}

func TestLogPane_ScrollPageDown_AtMaxEnablesFollow(t *testing.T) {
	p := newTestPane(100_000)
	for i := 0; i < 50; i++ {
		p.AppendLine(int64(i), "stdout", fmt.Sprintf("line %d", i))
	}
	p.Follow = false
	ms := p.maxScroll()
	p.Scroll = ms - 1 // one before the end

	p.scrollPageDown(ms)
	assert.True(t, p.Follow)
	assert.Equal(t, ms, p.Scroll)
}

// ---- EvictBelow edge case ----

func TestLogPane_EvictBelow_AllLinesEvicted(t *testing.T) {
	p := newTestPane(100_000)
	for i := 0; i < 5; i++ {
		p.AppendLine(int64(i), "stdout", fmt.Sprintf("line %d", i))
	}
	// Evict beyond all lines.
	p.EvictBelow(100)
	assert.Equal(t, 0, len(p.Lines))
	assert.Equal(t, 100, p.FirstLoadedLine)
}

// ---- RenderLines ----

func TestLogPane_RenderLines_WithLineNumbers(t *testing.T) {
	p := newTestPane(100_000)
	for i := 0; i < 5; i++ {
		p.AppendLine(int64(i), "stdout", fmt.Sprintf("line %d", i))
	}
	p.Scroll = 0

	var b strings.Builder
	p.RenderLines(&b, false, false)
	out := b.String()
	assert.NotEmpty(t, out)
	assert.Contains(t, out, "line 0")
}

func TestLogPane_RenderLines_WithoutLineNumbers(t *testing.T) {
	p := newTestPane(100_000)
	p.SetLineNumbers(false)
	for i := 0; i < 5; i++ {
		p.AppendLine(int64(i), "stdout", fmt.Sprintf("text %d", i))
	}
	p.Scroll = 0

	var b strings.Builder
	p.RenderLines(&b, false, false)
	out := b.String()
	assert.NotEmpty(t, out)
	assert.Contains(t, out, "text 0")
}

func TestLogPane_RenderLines_DimContent(t *testing.T) {
	p := newTestPane(100_000)
	p.AppendLine(0, "stdout", "dimmed line")
	p.Scroll = 0

	var b strings.Builder
	p.RenderLines(&b, true, false)
	assert.NotEmpty(t, b.String())
}

func TestLogPane_RenderLines_LoadingOlder(t *testing.T) {
	p := newTestPane(100_000)
	p.AppendLine(0, "stdout", "some line")
	p.Scroll = 0

	var b strings.Builder
	p.RenderLines(&b, false, true)
	out := b.String()
	assert.Contains(t, out, "Loading older logs")
}

func TestLogPane_RenderLines_StderrAndSystem(t *testing.T) {
	p := newTestPane(100_000)
	p.AppendLine(0, "stderr", "error line")
	p.AppendLine(1, "system", "system line")
	p.Scroll = 0

	var b strings.Builder
	p.RenderLines(&b, false, false)
	assert.NotEmpty(t, b.String())
}

func TestLogPane_RenderLines_HScrollOffset(t *testing.T) {
	p := newTestPane(100_000)
	p.AppendLine(0, "stdout", fmt.Sprintf("%-200s", "hello"))
	p.Scroll = 0
	p.HScroll = 10 // non-zero horizontal scroll

	var b strings.Builder
	p.RenderLines(&b, false, false)
	assert.NotEmpty(t, b.String())
}

func TestLogPane_RenderLines_WithoutLineNumbersHScroll(t *testing.T) {
	p := newTestPane(100_000)
	p.SetLineNumbers(false)
	p.AppendLine(0, "stdout", fmt.Sprintf("%-200s", "hello"))
	p.Scroll = 0
	p.HScroll = 10

	var b strings.Builder
	p.RenderLines(&b, false, false)
	assert.NotEmpty(t, b.String())
}

// ---- styleForStream ----

func TestStyleForStream_AllStreams(t *testing.T) {
	stdout := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffffff"))
	stderr := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000"))
	system := lipgloss.NewStyle().Foreground(lipgloss.Color("#aaaaaa"))

	assert.Equal(t, stderr, styleForStream("stderr", stdout, stderr, system))
	assert.Equal(t, system, styleForStream("system", stdout, stderr, system))
	assert.Equal(t, stdout, styleForStream("stdout", stdout, stderr, system))
	assert.Equal(t, stdout, styleForStream("unknown", stdout, stderr, system))
}

// ---- trimTrailingRuneIfClipped ----

func TestTrimTrailingRuneIfClipped_NotClipped(t *testing.T) {
	assert.Equal(t, "hello", trimTrailingRuneIfClipped("hello", false))
}

func TestTrimTrailingRuneIfClipped_Clipped(t *testing.T) {
	got := trimTrailingRuneIfClipped("hello", true)
	assert.Equal(t, "hell", got)
}

func TestTrimTrailingRuneIfClipped_EmptyClipped(t *testing.T) {
	got := trimTrailingRuneIfClipped("", true)
	assert.Equal(t, "", got)
}

// ---- composeLineContent ----

func TestComposeLineContent_NoIndicators(t *testing.T) {
	ts := lipgloss.NewStyle()
	ls := lipgloss.NewStyle()
	rs := lipgloss.NewStyle()
	out := composeLineContent("hello", false, false, ls, rs, ts)
	assert.Contains(t, out, "hello")
}

func TestComposeLineContent_LeftIndicator(t *testing.T) {
	ts := lipgloss.NewStyle()
	ls := lipgloss.NewStyle()
	rs := lipgloss.NewStyle()
	out := composeLineContent("world", true, false, ls, rs, ts)
	assert.Contains(t, out, "world")
	assert.Contains(t, out, "◂")
}

func TestComposeLineContent_RightIndicator(t *testing.T) {
	ts := lipgloss.NewStyle()
	ls := lipgloss.NewStyle()
	rs := lipgloss.NewStyle()
	out := composeLineContent("world", false, true, ls, rs, ts)
	assert.Contains(t, out, "world")
	assert.Contains(t, out, "▸")
}

func TestComposeLineContent_BothIndicators(t *testing.T) {
	ts := lipgloss.NewStyle()
	ls := lipgloss.NewStyle()
	rs := lipgloss.NewStyle()
	out := composeLineContent("mid", true, true, ls, rs, ts)
	assert.Contains(t, out, "◂")
	assert.Contains(t, out, "mid")
	assert.Contains(t, out, "▸")
}

// ---- padLineContent ----

func TestPadLineContent_ShortContent(t *testing.T) {
	ps := lipgloss.NewStyle()
	out := padLineContent("hi", 10, ps)
	// Should be padded to width 10.
	vis := lipgloss.Width(out)
	assert.Equal(t, 10, vis)
}

func TestPadLineContent_ExactWidth(t *testing.T) {
	ps := lipgloss.NewStyle()
	out := padLineContent("hello", 5, ps)
	assert.Equal(t, "hello", out)
}

func TestPadLineContent_OverWidth(t *testing.T) {
	ps := lipgloss.NewStyle()
	out := padLineContent("hello world", 5, ps)
	// No truncation — content returned as-is when wider than target.
	assert.Equal(t, "hello world", out)
}

// ---- sliceRowText ----

func TestSliceRowText_HScrollDisabled(t *testing.T) {
	p := newTestPane(100_000)
	p.Cfg.HScroll = false
	p.HScroll = 10
	sliced, clipped := p.sliceRowText("hello world", 5)
	assert.Equal(t, "hello world", sliced)
	assert.False(t, clipped)
}

func TestSliceRowText_HScrollEnabled(t *testing.T) {
	p := newTestPane(100_000)
	p.Cfg.HScroll = true
	p.HScroll = 5
	sliced, _ := p.sliceRowText("hello world", 5)
	// Slice starts at col 5 → " worl" (5 chars), then trailing rune trimmed
	// because the source string was clipped on the right.
	assert.NotEqual(t, "hello world", sliced)
}

// ---- clampScroll ----

func TestLogPane_ClampScroll_FollowSnapToMaxScroll(t *testing.T) {
	p := newTestPane(100_000)
	for i := 0; i < 50; i++ {
		p.AppendLine(int64(i), "stdout", fmt.Sprintf("line %d", i))
	}
	p.Follow = true
	p.Scroll = 0

	// Trigger clampScroll via SetHeaderHeight.
	p.SetHeaderHeight(4)
	assert.Equal(t, p.maxScroll(), p.Scroll)
}

func TestLogPane_ClampScroll_NonFollow_AboveMax(t *testing.T) {
	p := newTestPane(100_000)
	for i := 0; i < 10; i++ {
		p.AppendLine(int64(i), "stdout", fmt.Sprintf("line %d", i))
	}
	p.Follow = false
	ms := p.maxScroll()
	p.Scroll = ms + 100 // way above max

	// Trigger clampScroll via SetHeaderHeight.
	p.SetHeaderHeight(p.HeaderH)
	assert.Equal(t, ms, p.Scroll)
}

func TestLogPane_ClampScroll_NonFollow_Negative(t *testing.T) {
	p := newTestPane(100_000)
	p.AppendLine(0, "stdout", "line")
	p.Follow = false
	p.Scroll = -5

	p.SetHeaderHeight(p.HeaderH)
	assert.Equal(t, 0, p.Scroll)
}

// ---- VisibleLines edge case ----

func TestLogPane_VisibleLines_AtLeastOne(t *testing.T) {
	p := newTestPane(100_000)
	// Height smaller than header → clamped to 1.
	p.SetSize(80, 1)
	p.SetHeaderHeight(10)
	assert.Equal(t, 1, p.VisibleLines())
}

// ---- scrollDown ----

func TestLogPane_ScrollDown_AtMax_EnablesFollow(t *testing.T) {
	p := newTestPane(100_000)
	for i := 0; i < 50; i++ {
		p.AppendLine(int64(i), "stdout", fmt.Sprintf("line %d", i))
	}
	p.Follow = false
	ms := p.maxScroll()
	p.Scroll = ms // already at max

	p.scrollDown(ms) // scroll when already at ms → follow should be set
	assert.True(t, p.Follow)
}

func TestLogPane_ScrollDown_BelowMax_Advances(t *testing.T) {
	p := newTestPane(100_000)
	for i := 0; i < 50; i++ {
		p.AppendLine(int64(i), "stdout", fmt.Sprintf("line %d", i))
	}
	p.Follow = false
	ms := p.maxScroll()
	p.Scroll = 0

	p.scrollDown(ms)
	assert.Equal(t, 1, p.Scroll)
}

// ---- LogContentWidth edge cases ----

func TestLogPane_LogContentWidth_NoLineNumbers_Narrow(t *testing.T) {
	p := NewPane(Config{LineNumbers: false})
	p.SetSize(5, 30) // width 5 − 2 = 3 < 10 → clamped to 10
	assert.Equal(t, 10, p.LogContentWidth())
}

func TestLogPane_LogContentWidth_WithLineNumbers_Narrow(t *testing.T) {
	p := NewPane(Config{LineNumbers: true})
	p.SetSize(5, 30) // very small → clamped to 10
	assert.Equal(t, 10, p.LogContentWidth())
}

func TestLogPane_LogContentWidth_WithLineNumbers_Normal(t *testing.T) {
	p := NewPane(Config{LineNumbers: true})
	p.SetSize(80, 30)
	w := p.LogContentWidth()
	assert.Greater(t, w, 10)
}

// ---- HandleKeyScroll with HScroll disabled ----

func TestLogPane_HandleKeyScroll_HScrollDisabled_HKeys_NotConsumed(t *testing.T) {
	p := newTestPane(100_000)
	p.Cfg.HScroll = false

	assert.False(t, p.HandleKeyScroll("left"))
	assert.False(t, p.HandleKeyScroll("right"))
}

// ---- AppendLine FirstLoadedLine not reset when already set ----

func TestLogPane_AppendLine_FirstLoadedLineNotOverriddenWhenSet(t *testing.T) {
	p := newTestPane(100_000)
	p.SetFirstLoadedLine(10)
	// FirstLoadedLine is already set; subsequent AppendLine calls must not reset it to 0.
	p.AppendLine(10, "stdout", "first")
	p.AppendLine(11, "stdout", "second")
	assert.Equal(t, 10, p.FirstLoadedLine)
}

// ---- effectiveEndPadding capped at half VisibleLines ----

func TestLogPane_EffectiveEndPadding_CappedAtHalfVisible(t *testing.T) {
	p := NewPane(Config{EndPadding: 100})
	p.SetSize(80, 10)    // VisibleLines = 10; cap = 5
	p.SetHeaderHeight(0) // ensure no header shrinkage
	ep := p.effectiveEndPadding()
	assert.LessOrEqual(t, ep, p.VisibleLines()/2)
}

// ---- JumpToLine ----

func TestLogPane_JumpToLine_InBuffer_CentresAndHighlights(t *testing.T) {
	p := newTestPane(100_000)
	p.SetFirstLoadedLine(100)
	for i := 0; i < 50; i++ {
		p.AppendLine(int64(100+i), "stdout", "x")
	}
	p.Follow = true
	p.Scroll = 0

	p.JumpToLine(120)
	if p.HighlightLine != 120 {
		t.Fatalf("HighlightLine: got %d want 120", p.HighlightLine)
	}
	if p.Follow {
		t.Fatal("Follow should be cleared after jump")
	}
	// Scroll should target roughly the middle of the visible window.
	if p.Scroll < 0 || p.Scroll > p.maxScroll() {
		t.Fatalf("Scroll out of range: %d (max %d)", p.Scroll, p.maxScroll())
	}
}

func TestLogPane_JumpToLine_OutOfBuffer_OnlyRecordsHighlight(t *testing.T) {
	p := newTestPane(100_000)
	p.SetFirstLoadedLine(100)
	for i := 0; i < 5; i++ {
		p.AppendLine(int64(100+i), "stdout", "y")
	}
	prevScroll := p.Scroll

	p.JumpToLine(9999)
	if p.HighlightLine != 9999 {
		t.Fatalf("HighlightLine: got %d want 9999", p.HighlightLine)
	}
	// Scroll should be unchanged because the line is outside the buffer.
	if p.Scroll != prevScroll {
		t.Fatalf("Scroll should be untouched: got %d want %d", p.Scroll, prevScroll)
	}
}

func TestLogPane_JumpToLine_ClampsTargetAtZero(t *testing.T) {
	p := newTestPane(100_000)
	p.SetFirstLoadedLine(100)
	for i := 0; i < 50; i++ {
		p.AppendLine(int64(100+i), "stdout", "z")
	}

	// Line near the start of the buffer would compute a negative target.
	p.JumpToLine(101)
	if p.Scroll != 0 {
		t.Fatalf("Scroll should be clamped to 0: got %d", p.Scroll)
	}
	if p.HighlightLine != 101 {
		t.Fatalf("HighlightLine: got %d want 101", p.HighlightLine)
	}
}

func TestLogPane_JumpToLine_ClampsTargetAtMaxScroll(t *testing.T) {
	p := newTestPane(100_000)
	p.SetFirstLoadedLine(0)
	for i := 0; i < 100; i++ {
		p.AppendLine(int64(i), "stdout", "w")
	}

	// Jump near the very end — target before clamping exceeds maxScroll.
	p.JumpToLine(99)
	ms := p.maxScroll()
	if p.Scroll > ms {
		t.Fatalf("Scroll exceeded maxScroll: %d > %d", p.Scroll, ms)
	}
}

// ---- ClearHighlight ----

func TestLogPane_ClearHighlight(t *testing.T) {
	p := newTestPane(100_000)
	p.HighlightLine = 42
	p.ClearHighlight()
	if p.HighlightLine != 0 {
		t.Fatalf("HighlightLine: got %d want 0", p.HighlightLine)
	}
}

// ---- PrependLines: negative firstLine clamped to zero ----

func TestLogPane_PrependLines_NegativeFirstLineClamped(t *testing.T) {
	p := newTestPane(100_000)
	p.AppendLine(0, "stdout", "existing")
	pre := []Line{{Stream: "stdout", Text: "old"}}
	p.PrependLines(pre, -5)
	assert.Equal(t, 0, p.FirstLoadedLine)
}
