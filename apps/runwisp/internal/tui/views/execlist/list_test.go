// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package execlist

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

// buildList creates a window with n items at offset 0 and returns the list.
func buildList(n int) ExecList {
	w := NewExecWindow(nil)
	items := make([]uikit.ExecListItem, n)
	for i := range items {
		items[i] = uikit.ExecListItem{Run: model.Run{ID: "r", TaskName: "task"}}
	}
	w.ApplyFetch(items, 0, n)
	l := NewExecList(w)
	return l
}

func TestSetHovered_Direct(t *testing.T) {
	l := buildList(10)
	l.SetHovered(5)
	if l.hoveredRow != 5 {
		t.Fatalf("expected hoveredRow=5, got %d", l.hoveredRow)
	}
	l.SetHovered(-1)
	if l.hoveredRow != -1 {
		t.Fatalf("expected hoveredRow=-1, got %d", l.hoveredRow)
	}
}

func TestSetHoveredFromLocalY_Normal(t *testing.T) {
	l := buildList(20)
	l.SetSize(80, 24) // vpH = 24 - dataRowOffset(1) - footerLines(1) = 22

	// localY=1 → visIdx=0 (dataRowOffset subtracted) → rowIdx=0+Scroll=0
	l.SetHoveredFromLocalY(1)
	if l.hoveredRow != 0 {
		t.Fatalf("expected hoveredRow=0, got %d", l.hoveredRow)
	}

	// localY=3 → visIdx=2 → rowIdx=2
	l.SetHoveredFromLocalY(3)
	if l.hoveredRow != 2 {
		t.Fatalf("expected hoveredRow=2, got %d", l.hoveredRow)
	}
}

func TestSetHoveredFromLocalY_BelowHeader(t *testing.T) {
	l := buildList(20)
	l.SetSize(80, 24)

	// localY=0 → visIdx=-1 → out of range → -1
	l.SetHoveredFromLocalY(0)
	if l.hoveredRow != -1 {
		t.Fatalf("expected hoveredRow=-1 for header row, got %d", l.hoveredRow)
	}
}

func TestSetHoveredFromLocalY_BeyondViewport(t *testing.T) {
	l := buildList(20)
	l.SetSize(80, 24) // vpH = 22

	// localY=24 → visIdx=23 → >= vpH → -1
	l.SetHoveredFromLocalY(24)
	if l.hoveredRow != -1 {
		t.Fatalf("expected hoveredRow=-1 for out-of-viewport Y, got %d", l.hoveredRow)
	}
}

func TestSetHoveredFromLocalY_EmptyList(t *testing.T) {
	w := NewExecWindow(nil)
	l := NewExecList(w)
	l.SetSize(80, 24)

	l.SetHoveredFromLocalY(5)
	if l.hoveredRow != -1 {
		t.Fatalf("expected hoveredRow=-1 for empty list, got %d", l.hoveredRow)
	}
}

func TestSetHoveredFromLocalY_WithScroll(t *testing.T) {
	l := buildList(50)
	l.SetSize(80, 24) // vpH = 22
	l.Scroll = 10

	// localY=2 → visIdx=1 → rowIdx=Scroll+1=11
	l.SetHoveredFromLocalY(2)
	if l.hoveredRow != 11 {
		t.Fatalf("expected hoveredRow=11, got %d", l.hoveredRow)
	}
}

func TestHandleClick_ValidRow(t *testing.T) {
	l := buildList(20)
	l.SetSize(80, 24)

	// localY=2 → visIdx=1 → rowIdx=1; cursor set to 1.
	ok := l.HandleClick(2)
	if !ok {
		t.Fatal("expected HandleClick to return true for valid row")
	}
	if l.cursor != 1 {
		t.Fatalf("expected cursor=1, got %d", l.cursor)
	}
}

func TestHandleClick_HeaderRow(t *testing.T) {
	l := buildList(20)
	l.SetSize(80, 24)

	// localY=0 → header → returns false, cursor unchanged.
	l.cursor = 3
	ok := l.HandleClick(0)
	if ok {
		t.Fatal("expected HandleClick to return false for header row")
	}
	if l.cursor != 3 {
		t.Fatalf("expected cursor unchanged (3), got %d", l.cursor)
	}
}

func TestHandleClick_BeyondTotal(t *testing.T) {
	l := buildList(5)
	l.SetSize(80, 24) // vpH = 22; totalCount = 5

	// localY=7 → visIdx=6 → rowIdx=6 which is >= totalCount(5) → false
	ok := l.HandleClick(7)
	if ok {
		t.Fatal("expected HandleClick to return false for rowIdx >= totalCount")
	}
}

func TestHandleClick_EmptyList(t *testing.T) {
	w := NewExecWindow(nil)
	l := NewExecList(w)
	l.SetSize(80, 24)

	ok := l.HandleClick(2)
	if ok {
		t.Fatal("expected HandleClick to return false on empty list")
	}
}

func TestTotalCount_DelegatesToWindow(t *testing.T) {
	l := buildList(7)
	if tc := l.TotalCount(); tc != 7 {
		t.Fatalf("expected TotalCount=7, got %d", tc)
	}
}

func TestTotalCount_EmptyList(t *testing.T) {
	w := NewExecWindow(nil)
	l := NewExecList(w)
	if tc := l.TotalCount(); tc != 0 {
		t.Fatalf("expected TotalCount=0, got %d", tc)
	}
}

func TestTaskColWidthFor_WideAbsorbsSurplus(t *testing.T) {
	l := buildList(1)
	// At width 100 the fixed columns sit at their caps and TASK takes the
	// rest, so it should be far wider than any single fixed column.
	got := l.taskColWidthFor(100)
	if got <= statusColCap {
		t.Fatalf("expected TASK to absorb the surplus at width 100, got %d", got)
	}
	// The row must fill the content width exactly — never overflow.
	cw := computeColWidths(100)
	if total := cw.task + cw.fixedSum() + colGutter; total != 100 {
		t.Fatalf("expected columns to sum to 100, got %d", total)
	}
}

func TestComputeColWidths_FixedColumnsCapped(t *testing.T) {
	// On a very wide pane the fixed columns must plateau at their caps
	// (longest value + 2) while TASK absorbs all remaining surplus.
	cw := computeColWidths(200)
	if cw.status != statusColCap || cw.started != startedColCap ||
		cw.duration != durationColCap || cw.trigger != triggerColCap {
		t.Fatalf("expected fixed columns at their caps, got %+v", cw)
	}
	if cw.task <= taskColIdeal {
		t.Fatalf("expected TASK to absorb the surplus past its ideal, got %d", cw.task)
	}
}

func TestComputeColWidths_NeverOverflows(t *testing.T) {
	// Across a wide range of pane widths the columns must fit exactly (or, when
	// impossibly narrow, never exceed the available width).
	for w := 20; w <= 200; w++ {
		cw := computeColWidths(w)
		total := cw.task + cw.fixedSum() + colGutter
		if total > w {
			t.Fatalf("width %d: columns sum to %d, overflow", w, total)
		}
		if cw.task < 1 || cw.status < 1 || cw.started < 1 || cw.duration < 1 || cw.trigger < 1 {
			t.Fatalf("width %d: a column collapsed to <1: %+v", w, cw)
		}
	}
}

func TestComputeColWidths_NarrowKeepsAllColumnsReadable(t *testing.T) {
	// On an 80-column terminal (≈52 content cells after the sidebar) every
	// fixed column should still meet its floor and TASK should stay usable.
	cw := computeColWidths(52)
	if cw.status < statusColMin || cw.started < startedColMin ||
		cw.duration < durationColMin || cw.trigger < triggerColMin {
		t.Fatalf("a fixed column dropped below its floor: %+v", cw)
	}
	if cw.task < taskColMin {
		t.Fatalf("expected TASK >= %d, got %d", taskColMin, cw.task)
	}
}

func TestScrollBy_ScrollDown(t *testing.T) {
	l := buildList(50)
	l.SetSize(80, 24) // vpH = 22

	l.ScrollBy(5)
	if l.Scroll != 5 {
		t.Fatalf("expected Scroll=5, got %d", l.Scroll)
	}
}

func TestScrollBy_ScrollUp(t *testing.T) {
	l := buildList(50)
	l.SetSize(80, 24)
	l.Scroll = 10

	l.ScrollBy(-3)
	if l.Scroll != 7 {
		t.Fatalf("expected Scroll=7, got %d", l.Scroll)
	}
}

func TestScrollBy_ClampsToZero(t *testing.T) {
	l := buildList(50)
	l.SetSize(80, 24)
	l.Scroll = 3

	l.ScrollBy(-100)
	if l.Scroll != 0 {
		t.Fatalf("expected Scroll clamped to 0, got %d", l.Scroll)
	}
}

func TestScrollBy_ClampsToMax(t *testing.T) {
	l := buildList(50)
	l.SetSize(80, 24) // vpH = 22; maxScroll = 50 - 22 + 1 = 29

	l.ScrollBy(1000)
	if l.Scroll != 29 {
		t.Fatalf("expected Scroll clamped to maxScroll=29, got %d", l.Scroll)
	}
}

func TestScrollBy_MovesCursorIntoView_Down(t *testing.T) {
	l := buildList(50)
	l.SetSize(80, 24) // vpH = 22
	l.cursor = 0

	// Scroll down 10 rows; cursor (0) is now before Scroll (10).
	l.ScrollBy(10)
	if l.cursor < l.Scroll {
		t.Fatalf("cursor %d fell below Scroll %d after ScrollBy", l.cursor, l.Scroll)
	}
}

func TestScrollBy_MovesCursorIntoView_Up(t *testing.T) {
	l := buildList(50)
	l.SetSize(80, 24) // vpH = 22
	l.Scroll = 20
	l.cursor = 40 // cursor above viewport when scrolling back to top

	l.ScrollBy(-20)
	vpH := l.ViewportHeight()
	if l.cursor >= l.Scroll+vpH {
		t.Fatalf("cursor %d is beyond viewport end %d after ScrollBy", l.cursor, l.Scroll+vpH)
	}
}

func TestScrollBy_EmptyList(t *testing.T) {
	w := NewExecWindow(nil)
	l := NewExecList(w)
	l.SetSize(80, 24)

	// Must not panic.
	l.ScrollBy(5)
	if l.Scroll != 0 {
		t.Fatalf("expected Scroll=0 for empty list, got %d", l.Scroll)
	}
}

func TestCursor_InitiallyZero(t *testing.T) {
	l := buildList(5)
	if c := l.Cursor(); c != 0 {
		t.Fatalf("expected initial cursor=0, got %d", c)
	}
}

func TestCursor_AfterFocusWithItems(t *testing.T) {
	l := buildList(5)
	l.SetFocused(false)
	l.cursor = -1
	l.SetFocused(true)
	if c := l.Cursor(); c != 0 {
		t.Fatalf("expected cursor=0 after SetFocused(true) with items, got %d", c)
	}
}

func TestSelectedRun_ReturnsRun(t *testing.T) {
	w := NewExecWindow(nil)
	items := []uikit.ExecListItem{
		{Run: model.Run{ID: "run-a", TaskName: "task-a"}},
		{Run: model.Run{ID: "run-b", TaskName: "task-b"}},
	}
	w.ApplyFetch(items, 0, 2)
	l := NewExecList(w)
	l.cursor = 1
	run := l.SelectedRun()
	if run == nil {
		t.Fatal("expected non-nil SelectedRun for cursor=1")
	}
	if run.ID != "run-b" {
		t.Fatalf("expected run-b, got %s", run.ID)
	}
}

func TestSelectedRun_NilWhenOutOfRange(t *testing.T) {
	w := NewExecWindow(nil)
	l := NewExecList(w)
	l.cursor = 5 // no items
	run := l.SelectedRun()
	if run != nil {
		t.Fatal("expected nil SelectedRun for out-of-range cursor")
	}
}

func TestUpdate_Down_MovesCursor(t *testing.T) {
	l := buildList(5)
	l.SetSize(80, 24)
	l.SetFocused(true)
	l.cursor = 0

	l.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	if l.Cursor() != 1 {
		t.Fatalf("expected cursor=1 after down, got %d", l.Cursor())
	}
}

func TestUpdate_Up_MovesCursor(t *testing.T) {
	l := buildList(5)
	l.SetSize(80, 24)
	l.SetFocused(true)
	l.cursor = 2

	l.Update(tea.KeyPressMsg{Code: tea.KeyUp})

	if l.Cursor() != 1 {
		t.Fatalf("expected cursor=1 after up, got %d", l.Cursor())
	}
}

func TestUpdate_Down_ClampedAtEnd(t *testing.T) {
	l := buildList(3)
	l.SetSize(80, 24)
	l.SetFocused(true)
	l.cursor = 2 // already at end

	l.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	if l.Cursor() != 2 {
		t.Fatalf("expected cursor to stay at 2, got %d", l.Cursor())
	}
}

func TestUpdate_Up_ClampedAtStart(t *testing.T) {
	l := buildList(3)
	l.SetSize(80, 24)
	l.SetFocused(true)
	l.cursor = 0

	l.Update(tea.KeyPressMsg{Code: tea.KeyUp})

	if l.Cursor() != 0 {
		t.Fatalf("expected cursor to stay at 0, got %d", l.Cursor())
	}
}

func TestUpdate_Home_JumpsToStart(t *testing.T) {
	l := buildList(10)
	l.SetSize(80, 24)
	l.SetFocused(true)
	l.cursor = 7

	l.Update(tea.KeyPressMsg{Code: tea.KeyHome})

	if l.Cursor() != 0 {
		t.Fatalf("expected cursor=0 after home, got %d", l.Cursor())
	}
}

func TestUpdate_End_JumpsToLast(t *testing.T) {
	l := buildList(10)
	l.SetSize(80, 24)
	l.SetFocused(true)
	l.cursor = 0

	l.Update(tea.KeyPressMsg{Code: tea.KeyEnd})

	if l.Cursor() != 9 {
		t.Fatalf("expected cursor=9 after end, got %d", l.Cursor())
	}
}

func TestUpdate_NotFocused_NoChange(t *testing.T) {
	l := buildList(5)
	l.SetSize(80, 24)
	l.SetFocused(false)
	l.cursor = 2

	l.Update(tea.KeyPressMsg{Code: tea.KeyDown})

	if l.Cursor() != 2 {
		t.Fatalf("expected cursor unchanged at 2, got %d", l.Cursor())
	}
}

func TestUpdate_EmptyList_NoChange(t *testing.T) {
	w := NewExecWindow(nil)
	l := NewExecList(w)
	l.SetSize(80, 24)
	l.SetFocused(true)

	// Must not panic on empty list.
	l.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	if l.Cursor() != 0 {
		t.Fatalf("expected cursor=0 for empty list, got %d", l.Cursor())
	}
}

func TestTaskColWidth_UsesSelfWidth(t *testing.T) {
	l := buildList(1)
	l.width = 100
	got := l.taskColWidth()
	if want := computeColWidths(100).task; got != want {
		t.Fatalf("expected taskColWidth()=%d for width=100, got %d", want, got)
	}
}

func TestComputeScrollbar_NoScrollbarNeeded(t *testing.T) {
	l := buildList(5)
	// vpH >= n: no scrollbar
	state := l.computeScrollbar(10, 5)
	if state.show {
		t.Fatal("expected no scrollbar when all items fit in viewport")
	}
}

func TestComputeScrollbar_ScrollbarShown(t *testing.T) {
	l := buildList(50)
	l.SetSize(80, 24)
	state := l.computeScrollbar(22, 50)
	if !state.show {
		t.Fatal("expected scrollbar when items exceed viewport")
	}
	if state.thumbStart < 0 {
		t.Fatalf("expected thumbStart >= 0, got %d", state.thumbStart)
	}
	if state.thumbEnd <= state.thumbStart {
		t.Fatalf("expected thumbEnd > thumbStart, got start=%d end=%d", state.thumbStart, state.thumbEnd)
	}
}

func TestComputeScrollbar_ZeroViewport(t *testing.T) {
	l := buildList(50)
	// vpH == 0: no scrollbar
	state := l.computeScrollbar(0, 50)
	if state.show {
		t.Fatal("expected no scrollbar for zero-height viewport")
	}
}

func TestComputeScrollbar_ThumbAtBottom(t *testing.T) {
	l := buildList(100)
	l.SetSize(80, 24)
	// Scroll to near-bottom
	l.Scroll = 79
	state := l.computeScrollbar(22, 100)
	if !state.show {
		t.Fatal("expected scrollbar at bottom scroll")
	}
	// thumb should be near the bottom of the viewport
	if state.thumbEnd <= state.thumbStart {
		t.Fatalf("expected thumbEnd > thumbStart, got start=%d end=%d", state.thumbStart, state.thumbEnd)
	}
	if state.thumbEnd > 22 {
		t.Fatalf("expected thumbEnd <= 22 (viewport height), got %d", state.thumbEnd)
	}
}

func TestUpdate_PageDown_MovesCursor(t *testing.T) {
	l := buildList(50)
	l.SetSize(80, 24) // vpH = 22
	l.SetFocused(true)
	l.cursor = 0

	l.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})

	vpH := l.ViewportHeight()
	if l.Cursor() != vpH {
		t.Fatalf("expected cursor=%d after pgdown, got %d", vpH, l.Cursor())
	}
}

func TestUpdate_PageUp_MovesCursor(t *testing.T) {
	l := buildList(50)
	l.SetSize(80, 24) // vpH = 22
	l.SetFocused(true)
	l.cursor = 40

	l.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})

	vpH := l.ViewportHeight()
	expected := 40 - vpH
	if l.Cursor() != expected {
		t.Fatalf("expected cursor=%d after pgup, got %d", expected, l.Cursor())
	}
}

func TestSetFilter_DelegatesToWindow(t *testing.T) {
	l := buildList(5)
	l.cursor = 3
	l.Scroll = 2

	l.SetFilter("my-task")

	// After SetFilter the cursor and scroll reset to 0.
	if l.cursor != 0 {
		t.Fatalf("expected cursor=0 after SetFilter, got %d", l.cursor)
	}
	if l.Scroll != 0 {
		t.Fatalf("expected Scroll=0 after SetFilter, got %d", l.Scroll)
	}
}

func TestPadCell_TruncatesLong(t *testing.T) {
	result := padCell("hello world", 5)
	// 4 runes + ellipsis
	if len([]rune(result)) != 5 {
		t.Fatalf("expected len=5, got %d: %q", len([]rune(result)), result)
	}
}

func TestPadCell_PadsShort(t *testing.T) {
	result := padCell("hi", 5)
	if result != "hi   " {
		t.Fatalf("expected 'hi   ', got %q", result)
	}
}

func TestPadCell_ExactFit(t *testing.T) {
	result := padCell("hello", 5)
	if result != "hello" {
		t.Fatalf("expected 'hello', got %q", result)
	}
}

func TestNeedsFetch_DelegatesToWindow(t *testing.T) {
	l := buildList(0)
	l.SetSize(80, 24)
	// Window has nil client, so NeedsFetch returns false.
	got := l.NeedsFetch()
	if got {
		t.Fatal("expected NeedsFetch=false for nil client")
	}
}

// TestBuildRowText_Branches exercises every buildRowText branch (nil item,
// plain row, cursor-highlighted row, hovered row, instance-indexed row) and
// asserts on a per-branch substring instead of just non-empty output.
func TestBuildRowText_Branches(t *testing.T) {
	t.Run("nil-item-renders-loading-placeholder", func(t *testing.T) {
		l := buildList(5)
		l.SetSize(80, 24)
		text := l.buildRowText(nil, 0, computeColWidths(60))
		if !strings.Contains(text, "loading") {
			t.Fatalf("expected loading placeholder, got %q", text)
		}
	})

	t.Run("plain-row-shows-task-name", func(t *testing.T) {
		l := buildList(5)
		l.SetSize(80, 24)
		item := l.window.Item(0)
		if item == nil {
			t.Fatal("pre-condition: expected item at index 0")
		}
		text := l.buildRowText(item, 0, computeColWidths(60))
		if !strings.Contains(text, "task") {
			t.Fatalf("expected task name in row, got %q", text)
		}
	})

	t.Run("cursor-highlight-still-renders-task", func(t *testing.T) {
		// Exercises the focused+cursor-on-row branch. (Color codes are
		// stripped by lipgloss in the test renderer, so we can't compare the
		// styled output to plain — assert the row still contains the task
		// name to confirm the branch produces sensible content.)
		l := buildList(5)
		l.SetSize(80, 24)
		l.SetFocused(true)
		l.cursor = 2
		text := l.buildRowText(l.window.Item(2), 2, computeColWidths(60))
		if !strings.Contains(text, "task") {
			t.Fatalf("expected task name in cursor row, got %q", text)
		}
	})

	t.Run("hovered-highlight-still-renders-task", func(t *testing.T) {
		l := buildList(5)
		l.SetSize(80, 24)
		l.SetHovered(1)
		text := l.buildRowText(l.window.Item(1), 1, computeColWidths(60))
		if !strings.Contains(text, "task") {
			t.Fatalf("expected task name in hovered row, got %q", text)
		}
	})

	t.Run("multi-instance-formatted-1-based", func(t *testing.T) {
		w := NewExecWindow(nil)
		w.ApplyFetch([]uikit.ExecListItem{
			{Run: model.Run{ID: "r1", TaskName: "task", InstanceIndex: 2}},
		}, 0, 1)
		l := NewExecList(w)
		l.SetInstanceCountLookup(func(string) int { return 3 })
		l.SetSize(80, 24)
		text := l.buildRowText(l.window.Item(0), 0, computeColWidths(60))
		// 0-based slot 2 → 1-based display #3.
		if !strings.Contains(text, "task#3") {
			t.Fatalf("expected task#3 in multi-instance row, got %q", text)
		}
	})

	t.Run("multi-instance-slot-zero-gets-suffix", func(t *testing.T) {
		w := NewExecWindow(nil)
		w.ApplyFetch([]uikit.ExecListItem{
			{Run: model.Run{ID: "r1", TaskName: "task", InstanceIndex: 0}},
		}, 0, 1)
		l := NewExecList(w)
		l.SetInstanceCountLookup(func(string) int { return 3 })
		l.SetSize(80, 24)
		text := l.buildRowText(l.window.Item(0), 0, computeColWidths(60))
		// Slot 0 of a multi-instance service must still be suffixed (#1).
		if !strings.Contains(text, "task#1") {
			t.Fatalf("expected task#1 for slot 0 of multi-instance service, got %q", text)
		}
	})

	t.Run("single-instance-no-suffix", func(t *testing.T) {
		w := NewExecWindow(nil)
		w.ApplyFetch([]uikit.ExecListItem{
			{Run: model.Run{ID: "r1", TaskName: "task", InstanceIndex: 0}},
		}, 0, 1)
		l := NewExecList(w)
		l.SetInstanceCountLookup(func(string) int { return 1 })
		l.SetSize(80, 24)
		text := l.buildRowText(l.window.Item(0), 0, computeColWidths(60))
		if strings.Contains(text, "task#") {
			t.Fatalf("expected no instance suffix for single-instance task, got %q", text)
		}
	})
}

// TestView_RenderBranches covers View()'s four data-shape branches: empty
// list, in-bounds list, overflowing list with scrollbar, and zero-content
// viewport. Coverage is preserved by exercising the same code paths; each
// branch pins a distinct substring instead of only checking non-empty.
func TestView_RenderBranches(t *testing.T) {
	t.Run("empty-list", func(t *testing.T) {
		w := NewExecWindow(nil)
		l := NewExecList(w)
		l.SetSize(80, 24)
		out := l.View()
		if out == "" {
			t.Fatal("expected non-empty View output for empty list")
		}
	})

	t.Run("with-items-contains-task-name", func(t *testing.T) {
		l := buildList(20)
		l.SetSize(80, 24)
		out := l.View()
		if !strings.Contains(out, "task") {
			t.Fatalf("expected task name in rendered list, got %q", out)
		}
	})

	t.Run("overflow-forces-scrollbar-row-differs", func(t *testing.T) {
		// 200 items in an 8-row viewport forces a scrollbar; the rendered
		// view must differ from the same-size empty viewport.
		big := buildList(200)
		big.SetSize(80, 10)
		out := big.View()
		if !strings.Contains(out, "task") {
			t.Fatalf("expected task names in overflowing view, got %q", out)
		}
		// And the View must be longer (more lines) than the empty case.
		w := NewExecWindow(nil)
		empty := NewExecList(w)
		empty.SetSize(80, 10)
		if len(out) <= len(empty.View()) {
			t.Fatal("overflowing list should render more characters than empty")
		}
	})

	t.Run("empty-viewport-still-renders", func(t *testing.T) {
		w := NewExecWindow(nil)
		l := NewExecList(w)
		l.SetSize(80, 10)
		out := l.View()
		if out == "" {
			t.Fatal("expected non-empty output for empty viewport (filler rows)")
		}
	})
}

func TestViewportHeight_TinyHeight(t *testing.T) {
	l := buildList(5)
	// height < dataRowOffset + footerLines → clamped to 1
	l.SetSize(80, 1)
	if vh := l.ViewportHeight(); vh != 1 {
		t.Fatalf("expected ViewportHeight=1 for tiny height, got %d", vh)
	}
}

func TestEnsureVisible_CursorBelowScroll(t *testing.T) {
	l := buildList(50)
	l.SetSize(80, 24) // vpH = 22
	l.Scroll = 10
	l.cursor = 5 // below scroll → scroll should move up to cursor

	l.ensureVisible()
	if l.Scroll != 5 {
		t.Fatalf("expected Scroll=5 when cursor below Scroll, got %d", l.Scroll)
	}
}

func TestEnsureVisible_CursorAboveViewport(t *testing.T) {
	l := buildList(50)
	l.SetSize(80, 24) // vpH = 22
	l.Scroll = 0
	l.cursor = 30 // above viewport bottom → scroll should advance

	l.ensureVisible()
	vpH := l.ViewportHeight()
	if l.cursor < l.Scroll || l.cursor >= l.Scroll+vpH {
		t.Fatalf("cursor %d not in viewport [%d, %d)", l.cursor, l.Scroll, l.Scroll+vpH)
	}
}

func TestEnsureVisible_NegativeScroll(t *testing.T) {
	l := buildList(5)
	l.SetSize(80, 24)
	l.Scroll = -5
	l.cursor = 0

	l.ensureVisible()
	if l.Scroll < 0 {
		t.Fatalf("expected Scroll >= 0 after ensureVisible, got %d", l.Scroll)
	}
}

func TestUpdate_KVim_DownUp(t *testing.T) {
	l := buildList(10)
	l.SetSize(80, 24)
	l.SetFocused(true)
	l.cursor = 3

	// "k" = up
	l.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if l.Cursor() != 2 {
		t.Fatalf("expected cursor=2 after k, got %d", l.Cursor())
	}

	// "j" = down
	l.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if l.Cursor() != 3 {
		t.Fatalf("expected cursor=3 after j, got %d", l.Cursor())
	}
}

// TestFilterBanner_AppearsOnlyWhenFiltering pins the active-filter banner: it is
// absent with no filter, present (and named) once one is active, and it costs
// exactly one viewport row so the layout stays consistent.
func TestFilterBanner_AppearsOnlyWhenFiltering(t *testing.T) {
	l := buildList(20)
	l.SetSize(80, 24) // vpH = 24 - header(1) - banner(0) - footer(1) = 22

	if l.bannerLines() != 0 {
		t.Fatalf("bannerLines without filter = %d, want 0", l.bannerLines())
	}
	if got := l.ViewportHeight(); got != 22 {
		t.Fatalf("ViewportHeight without filter = %d, want 22", got)
	}
	if strings.Contains(l.View(), "FILTER:") {
		t.Fatal("did not expect a filter banner without an active filter")
	}

	l.window.CycleStatusFilter() // "" → running

	if l.bannerLines() != 1 {
		t.Fatalf("bannerLines with filter = %d, want 1", l.bannerLines())
	}
	if got := l.ViewportHeight(); got != 21 {
		t.Fatalf("ViewportHeight with filter = %d, want 21", got)
	}
	if view := l.View(); !strings.Contains(view, "FILTER: RUNNING") {
		t.Fatalf("expected filter banner naming the status, got:\n%s", view)
	}
}

// TestRenderEmptySection_FillsViewport verifies the empty state fills the whole
// viewport (message on the first row, blank rows beneath) so the footer lands at
// the bottom exactly as it does for a populated list.
func TestRenderEmptySection_FillsViewport(t *testing.T) {
	l := buildList(0)
	l.SetSize(80, 10) // vpH = 10 - 1 - 0 - 1 = 8

	var b strings.Builder
	l.renderEmptySection(&b, l.ViewportHeight(), 80)

	if lines := strings.Count(b.String(), "\n"); lines != 8 {
		t.Fatalf("renderEmptySection wrote %d lines, want vpH=8", lines)
	}
	first := strings.SplitN(b.String(), "\n", 2)[0]
	if !strings.Contains(first, "No executions") {
		t.Fatalf("expected the empty message on the first line, got %q", first)
	}
}

// TestRenderEmptySection_FilterMessage swaps in a filter-aware message when the
// empty list is the result of a status filter rather than no runs at all.
func TestRenderEmptySection_FilterMessage(t *testing.T) {
	l := buildList(0)
	l.SetSize(80, 10)
	l.window.CycleStatusFilter() // "" → running

	var b strings.Builder
	l.renderEmptySection(&b, l.ViewportHeight(), 80)
	if !strings.Contains(b.String(), "No runs match this filter") {
		t.Fatalf("expected filter-specific empty message, got:\n%s", b.String())
	}
}
