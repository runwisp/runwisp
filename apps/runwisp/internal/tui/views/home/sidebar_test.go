// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package home

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeTasks(names ...string) []model.Task {
	tasks := make([]model.Task, len(names))
	for i, n := range names {
		tasks[i] = model.Task{Name: n}
	}
	return tasks
}

func makeGroupedTasks() []model.Task {
	return []model.Task{
		{Name: "alpha", Group: "group-a"},
		{Name: "bravo", Group: "group-a"},
		{Name: "charlie", Group: "group-b"},
	}
}

func TestBuildItems_NoTasks(t *testing.T) {
	items := buildItems(nil)
	// Should have at least Home, Info, Debug
	require.GreaterOrEqual(t, len(items), 3)
	assert.Equal(t, "Home", items[0].label)
}

func TestBuildItems_FlatTasks(t *testing.T) {
	tasks := makeTasks("alpha", "bravo")
	items := buildItems(tasks)
	labels := make([]string, len(items))
	for i, item := range items {
		labels[i] = item.label
	}
	assert.Contains(t, labels, "Home")
	assert.Contains(t, labels, "alpha")
	assert.Contains(t, labels, "bravo")
	assert.Contains(t, labels, "Info")
	assert.Contains(t, labels, "Debug")
}

func TestBuildGroupItems(t *testing.T) {
	tasks := makeGroupedTasks()
	groups := []string{"group-a", "group-b"}
	items := buildGroupItems(tasks, groups)

	assert.Equal(t, entryGroupHeader, items[0].kind)
	assert.Equal(t, "group-a", items[0].label)
	assert.Equal(t, entryTask, items[1].kind)
	assert.Equal(t, "alpha", items[1].label)
	assert.Equal(t, entryTask, items[2].kind)
	assert.Equal(t, "bravo", items[2].label)
	assert.Equal(t, entryGroupHeader, items[3].kind)
	assert.Equal(t, "group-b", items[3].label)
	assert.Equal(t, entryTask, items[4].kind)
	assert.Equal(t, "charlie", items[4].label)
}

func TestNewSidebar_BasicState(t *testing.T) {
	tasks := makeTasks("task1", "task2")
	s := NewSidebar("RunWisp", "0.1.0", "fp123", tasks)

	assert.Equal(t, uikit.PageHome, s.ActivePage())
	assert.True(t, s.focused)
	assert.Equal(t, -1, s.hovered)
}

func TestSidebar_SetFocused(t *testing.T) {
	s := NewSidebar("RunWisp", "0.1.0", "", makeTasks("t"))
	s.SetFocused(false)
	assert.False(t, s.focused)

	s.SetFocused(true)
	assert.True(t, s.focused)
}

func TestSidebar_SetHovered(t *testing.T) {
	s := NewSidebar("RunWisp", "0.1.0", "", makeTasks("t"))
	s.SetHovered(3)
	assert.Equal(t, 3, s.hovered)

	s.SetHovered(-1)
	assert.Equal(t, -1, s.hovered)
}

func TestSidebar_ActiveTask_WithNoTaskSelected(t *testing.T) {
	s := NewSidebar("RunWisp", "0.1.0", "", makeTasks("t1", "t2"))
	// Default selected=0 → Home which has no taskName
	assert.Equal(t, "", s.ActiveTask())
}

func TestSidebar_CursorTaskName(t *testing.T) {
	tasks := makeTasks("alpha", "bravo")
	s := NewSidebar("RunWisp", "0.1.0", "", tasks)
	s.SetSize(20, 20)

	// Move cursor to first task (index 1, after Home)
	s.cursor = 1
	assert.Equal(t, "alpha", s.CursorTaskName())
}

func TestSidebar_Update_Navigation(t *testing.T) {
	tasks := makeTasks("alpha", "bravo")
	s := NewSidebar("RunWisp", "0.1.0", "", tasks)
	s.SetSize(20, 20)
	s.cursor = 0

	s.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	assert.Equal(t, 1, s.cursor)

	s.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	assert.Equal(t, 0, s.cursor)

	s.Update(tea.KeyPressMsg{Code: 'G', Text: "G"})
	assert.Greater(t, s.cursor, 0)

	s.Update(tea.KeyPressMsg{Code: 'g', Text: "g"})
	assert.Equal(t, 0, s.cursor)

	s.Update(tea.KeyPressMsg{Code: tea.KeyEnd})
	lastCursor := s.cursor

	s.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
	assert.Equal(t, lastCursor, s.selected)
}

// TestSidebar_Update_PageKeys exercises the pgup/pgdown branches and the
// named arrow keys (up/down/home/end) so the cases shared with k/j/g/G are
// hit through both name forms.
func TestSidebar_Update_PageKeys(t *testing.T) {
	tasks := makeTasks("alpha", "bravo", "charlie", "delta", "echo", "foxtrot")
	s := NewSidebar("RunWisp", "0.1.0", "", tasks)
	s.SetSize(20, 8)
	s.cursor = 0

	s.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	assert.Greater(t, s.cursor, 0, "pgdown should advance the cursor")

	s.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	assert.Equal(t, 0, s.cursor, "pgup should return to top")

	s.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	assert.Equal(t, 1, s.cursor, "named Down arrow should advance cursor")

	s.Update(tea.KeyPressMsg{Code: tea.KeyUp})
	assert.Equal(t, 0, s.cursor, "named Up arrow should retreat cursor")

	s.Update(tea.KeyPressMsg{Code: tea.KeyHome})
	assert.Equal(t, 0, s.cursor, "named Home should reset cursor")
}

// TestSidebar_Update_UnhandledKeyReturnsNil verifies the default branch which
// produces no state change and returns nil.
func TestSidebar_Update_UnhandledKeyReturnsNil(t *testing.T) {
	tasks := makeTasks("a")
	s := NewSidebar("RunWisp", "0.1.0", "", tasks)
	s.cursor = 0
	got := s.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	assert.Nil(t, got)
	assert.Equal(t, 0, s.cursor)
}

// TestSidebar_Update_PressEnterSelects covers the "enter" branch (the existing
// navigation test only uses space).
func TestSidebar_Update_PressEnterSelects(t *testing.T) {
	tasks := makeTasks("alpha", "bravo")
	s := NewSidebar("RunWisp", "0.1.0", "", tasks)
	s.cursor = 1
	s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Equal(t, 1, s.selected)
}

func TestSidebar_Update_NotFocused_IgnoresInput(t *testing.T) {
	tasks := makeTasks("t1")
	s := NewSidebar("RunWisp", "0.1.0", "", tasks)
	s.SetFocused(false)
	s.cursor = 0

	s.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	assert.Equal(t, 0, s.cursor)
}

func TestSidebar_RowIndexAt(t *testing.T) {
	tasks := makeTasks("a", "b", "c")
	s := NewSidebar("RunWisp", "0.1.0", "", tasks)
	s.SetSize(20, 30)

	bh := s.brandHeight()

	// Y exactly at brandHeight → index 0
	idx := s.RowIndexAt(bh)
	assert.Equal(t, 0, idx)

	// Y below available content → -1
	idx = s.RowIndexAt(-1)
	assert.Equal(t, -1, idx)
}

func TestSidebar_HandleClick(t *testing.T) {
	tasks := makeTasks("a", "b")
	s := NewSidebar("RunWisp", "0.1.0", "", tasks)
	s.SetSize(20, 30)

	bh := s.brandHeight()
	// Click on first visible item (Home at index 0)
	s.HandleClick(bh)
	assert.Equal(t, 0, s.cursor)
	assert.Equal(t, 0, s.selected)
	assert.True(t, s.focused)
}

func TestSidebar_HandleClick_OnGroupHeader_NoChange(t *testing.T) {
	tasks := makeGroupedTasks()
	s := NewSidebar("RunWisp", "0.1.0", "", tasks)
	s.SetSize(20, 30)

	origCursor := s.cursor
	origSelected := s.selected

	// Find a group header item and compute what Y would hit it
	for i, item := range s.items {
		if item.kind == entryGroupHeader {
			y := s.brandHeight() + (i - s.scroll)
			s.HandleClick(y)
			// Cursor and selected should not change since group headers are not selectable
			assert.Equal(t, origCursor, s.cursor)
			assert.Equal(t, origSelected, s.selected)
			break
		}
	}
}

func TestTruncateToWidth(t *testing.T) {
	assert.Equal(t, "hello", truncateToWidth("hello", 10))
	assert.Equal(t, "hello", truncateToWidth("hello", 5))

	// Should truncate with ellipsis
	result := truncateToWidth("hello world", 6)
	assert.LessOrEqual(t, len([]rune(result)), 6)
	assert.Contains(t, result, "…")

	// Width = 1 → just ellipsis
	assert.Equal(t, "…", truncateToWidth("hi", 1))

	// Width <= 0 → return as-is (no truncation)
	assert.Equal(t, "hi", truncateToWidth("hi", 0))
}

func TestSidebar_ActivePage_OutOfBounds(t *testing.T) {
	s := NewSidebar("RunWisp", "0.1.0", "", nil)
	s.selected = -1
	assert.Equal(t, uikit.PageHome, s.ActivePage())

	s.selected = 999
	assert.Equal(t, uikit.PageHome, s.ActivePage())
}

// TestSidebar_ActiveTaskAndCursorTaskName_OutOfBounds covers the "return \"\""
// fallback branches when selected/cursor land outside the items slice.
func TestSidebar_ActiveTaskAndCursorTaskName_OutOfBounds(t *testing.T) {
	s := NewSidebar("RunWisp", "0.1.0", "", makeTasks("t1"))
	s.selected = -1
	assert.Equal(t, "", s.ActiveTask())
	s.selected = 999
	assert.Equal(t, "", s.ActiveTask())
	s.cursor = -1
	assert.Equal(t, "", s.CursorTaskName())
	s.cursor = 999
	assert.Equal(t, "", s.CursorTaskName())
}

// TestSidebar_Update_NonKeyMsgReturnsNil covers the "msg is not tea.KeyMsg"
// early-return branch.
func TestSidebar_Update_NonKeyMsgReturnsNil(t *testing.T) {
	s := NewSidebar("RunWisp", "0.1.0", "", makeTasks("a"))
	got := s.Update(tea.WindowSizeMsg{Width: 1, Height: 1})
	assert.Nil(t, got)
}

// TestSidebar_View_WithFingerprint covers the brandHeight=5 and fingerprint
// render branches in View.
func TestSidebar_View_WithFingerprint(t *testing.T) {
	s := NewSidebar("RunWisp", "0.1.0", "fp-abc", makeTasks("alpha"))
	s.SetSize(30, 15)
	out := s.View()
	assert.Contains(t, out, "RunWisp")
	assert.Contains(t, out, "fp-abc")
}

// TestSidebar_View_GroupHeaderAndStylesFire iterates focus/hover combinations
// so renderItem walks each style-switch arm.
func TestSidebar_View_GroupHeaderAndStylesFire(t *testing.T) {
	s := NewSidebar("RunWisp", "0.1.0", "", makeGroupedTasks())
	s.SetSize(40, 30)
	// focused + cursor + selected match same row
	s.cursor = 1
	s.selected = 1
	s.focused = true
	_ = s.View()
	// focused + cursor different from selected
	s.cursor = 2
	s.selected = 1
	_ = s.View()
	// focused + selected only (cursor elsewhere)
	s.cursor = 4
	s.selected = 1
	_ = s.View()
	// blurred + selected (no focus)
	s.focused = false
	s.selected = 1
	_ = s.View()
	// blurred + hovered branch
	s.selected = 0
	s.SetHovered(1)
	_ = s.View()
}

// TestSidebar_SkipGroupHeaders covers both directions and the clamp branches.
func TestSidebar_SkipGroupHeaders(t *testing.T) {
	s := NewSidebar("RunWisp", "0.1.0", "", makeGroupedTasks())
	s.SetSize(20, 30)
	// cursor on a group header — direction +1 should advance to a task
	for i, item := range s.items {
		if item.kind == entryGroupHeader {
			s.cursor = i
			s.skipGroupHeaders(1)
			assert.NotEqual(t, entryGroupHeader, s.items[s.cursor].kind)
			break
		}
	}
	// cursor far past the end → clamp to len-1
	s.cursor = 9999
	s.skipGroupHeaders(1)
	assert.Equal(t, len(s.items)-1, s.cursor)
	// cursor before zero → clamp to 0
	s.cursor = -5
	s.skipGroupHeaders(-1)
	assert.Equal(t, 0, s.cursor)
}

// TestSidebar_RowIndexAt_OutOfVisibleRange exercises localY < 0 and
// localY >= visibleHeight branches.
func TestSidebar_RowIndexAt_OutOfVisibleRange(t *testing.T) {
	s := NewSidebar("RunWisp", "0.1.0", "", makeTasks("a", "b"))
	s.SetSize(20, 30)
	bh := s.brandHeight()
	// localY < 0 (above the list)
	assert.Equal(t, -1, s.RowIndexAt(bh-1))
	// localY >= visibleHeight (below the list)
	assert.Equal(t, -1, s.RowIndexAt(bh+1000))
}

// TestSidebar_EnsureVisible_EmptyItemsResetsCursorAndScroll covers the
// len(items)==0 branch.
func TestSidebar_EnsureVisible_EmptyItemsResetsCursorAndScroll(t *testing.T) {
	s := NewSidebar("RunWisp", "0.1.0", "", nil)
	s.items = nil
	s.cursor = 5
	s.scroll = 5
	s.SetSize(20, 10)
	assert.Equal(t, 0, s.cursor)
	assert.Equal(t, 0, s.scroll)
}

func TestSidebar_FilterNarrowsToMatchingTasks(t *testing.T) {
	s := NewSidebar("RunWisp", "0.1.0", "", makeTasks("alpha", "beta", "alpaca"))
	s.SetSize(20, 20)

	require.False(t, s.Filtering(), "fresh sidebar is not filtering")
	s.StartFilter()
	assert.True(t, s.Filtering(), "StartFilter enters the sub-mode")
	assert.Equal(t, "", s.filter)

	s.FilterAppend("alp")
	assert.Equal(t, "alp", s.filter)

	// Only the two tasks containing "alp" survive; the cursor lands on a match.
	var names []string
	for _, it := range s.items {
		if it.kind == entryTask {
			names = append(names, it.taskName)
		}
	}
	assert.ElementsMatch(t, []string{"alpha", "alpaca"}, names)
	assert.Contains(t, []string{"alpha", "alpaca"}, s.CursorTaskName())

	// Backspace widens the query back to "al" — still both tasks.
	s.FilterBackspace()
	assert.Equal(t, "al", s.filter)
}

func TestSidebar_ViewRendersFilterLine(t *testing.T) {
	s := NewSidebar("RunWisp", "0.1.0", "", makeTasks("alpha", "beta"))
	s.SetSize(24, 20)
	s.StartFilter()
	s.FilterAppend("alp")

	out := s.View()
	assert.Contains(t, out, "alp", "the filter header echoes the active query")
}

func TestSidebar_FilterBackspaceEmptyIsNoOp(t *testing.T) {
	s := NewSidebar("RunWisp", "0.1.0", "", makeTasks("alpha"))
	s.SetSize(20, 20)
	s.StartFilter()
	s.FilterBackspace() // empty query → nothing to remove
	assert.Equal(t, "", s.filter)
}

func TestSidebar_StopFilterRestoresFullList(t *testing.T) {
	s := NewSidebar("RunWisp", "0.1.0", "", makeTasks("alpha", "beta"))
	s.SetSize(20, 20)
	s.StartFilter()
	s.FilterAppend("alp")
	require.Less(t, len(s.items), len(s.allItems), "filter narrows the list")

	s.StopFilter()
	assert.False(t, s.Filtering())
	assert.Equal(t, "", s.filter)
	assert.Equal(t, len(s.allItems), len(s.items), "StopFilter restores every item")
}

func TestSidebar_SelectFilterCursorActivatesAndExits(t *testing.T) {
	s := NewSidebar("RunWisp", "0.1.0", "", makeTasks("alpha", "beta"))
	s.SetSize(20, 20)
	s.StartFilter()
	s.FilterAppend("beta")
	require.Equal(t, "beta", s.CursorTaskName())

	s.SelectFilterCursor()
	assert.False(t, s.Filtering(), "selecting exits filter mode")
	assert.Equal(t, "beta", s.ActiveTask(), "the cursor's task becomes the active selection")
}

func TestSidebar_RebuildPreservesSelectionAndExitsFilter(t *testing.T) {
	s := NewSidebar("RunWisp", "0.1.0", "", makeTasks("alpha", "beta", "gamma"))
	s.SetSize(20, 20)
	s.StartFilter()
	s.FilterAppend("beta")
	s.SelectFilterCursor()
	require.Equal(t, "beta", s.ActiveTask())

	// A reload that still contains "beta" keeps it selected and drops filter mode.
	s.Rebuild(makeTasks("alpha", "beta", "delta"))
	assert.False(t, s.Filtering())
	assert.Equal(t, "beta", s.ActiveTask(), "Rebuild preserves the active task when it survives")
}

// TestSidebar_EnsureVisible_ScrollClampAndCursorBounds runs the cursor<0 /
// cursor>=len(items) / scroll>maxScroll branches in ensureVisible.
func TestSidebar_EnsureVisible_ScrollClampAndCursorBounds(t *testing.T) {
	tasks := makeTasks("a", "b", "c", "d", "e", "f", "g", "h")
	s := NewSidebar("RunWisp", "0.1.0", "", tasks)
	s.SetSize(20, 6) // small height to force scroll
	// cursor below zero — ensureVisible should clamp to 0
	s.cursor = -3
	s.scroll = 100
	s.SetSize(20, 6)
	assert.GreaterOrEqual(t, s.cursor, 0)
	assert.GreaterOrEqual(t, s.scroll, 0)
	// cursor far past the end — ensureVisible should clamp to len-1
	s.cursor = 9999
	s.SetSize(20, 6)
	assert.Equal(t, len(s.items)-1, s.cursor)
}

func TestSidebar_SetUpdate_TracksAvailabilityAndLatest(t *testing.T) {
	s := NewSidebar("RunWisp", "0.1.0", "", makeTasks("a"))
	assert.False(t, s.VersionFocused())

	s.SetUpdate(true, "v0.2.0")
	assert.Equal(t, "v0.2.0", s.LatestVersion())
	assert.False(t, s.VersionFocused(), "SetUpdate alone does not move focus")

	s.FocusVersion()
	assert.True(t, s.VersionFocused())
}

// TestSidebar_SetUpdate_LosingAvailabilityDropsFocus covers the branch where a
// reload clears updateAvailable while the indicator held keyboard focus.
func TestSidebar_SetUpdate_LosingAvailabilityDropsFocus(t *testing.T) {
	s := NewSidebar("RunWisp", "0.1.0", "", makeTasks("a"))
	s.SetUpdate(true, "v0.2.0")
	s.FocusVersion()
	require.True(t, s.VersionFocused())

	s.SetUpdate(false, "")
	assert.False(t, s.VersionFocused())
}

// TestSidebar_FocusVersion_NoopWhenUnavailable covers FocusVersion's no-op
// branch when there is nothing to focus.
func TestSidebar_FocusVersion_NoopWhenUnavailable(t *testing.T) {
	s := NewSidebar("RunWisp", "0.1.0", "", makeTasks("a"))
	s.FocusVersion()
	assert.False(t, s.VersionFocused())
}

func TestSidebar_VersionRowAt(t *testing.T) {
	s := NewSidebar("RunWisp", "0.1.0", "", makeTasks("a"))
	assert.False(t, s.VersionRowAt(versionRow), "no update available → never hittable")

	s.SetUpdate(true, "v0.2.0")
	assert.True(t, s.VersionRowAt(versionRow))
	assert.False(t, s.VersionRowAt(versionRow+1))
}

// TestSidebar_Update_VersionFocused_Navigation walks every key branch that
// touches versionFocused: stepping up off the first item onto the indicator,
// stepping back down into the list, staying put at the very top, and every
// other navigation key dropping focus back to the list.
func TestSidebar_Update_VersionFocused_Navigation(t *testing.T) {
	tasks := makeTasks("alpha", "bravo")
	s := NewSidebar("RunWisp", "0.1.0", "", tasks)
	s.SetSize(20, 20)
	s.SetUpdate(true, "v0.2.0")
	s.cursor = 0

	// "up" from the first item lands on the indicator.
	s.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	assert.True(t, s.VersionFocused())

	// Already focused: "up" again is a no-op (stays at the very top).
	got := s.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
	assert.Nil(t, got)
	assert.True(t, s.VersionFocused())

	// "enter"/"space" while focused is swallowed here — the model owns the dialog.
	got = s.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	assert.Nil(t, got)
	assert.Equal(t, 0, s.selected)

	// "down" steps back off the indicator into the list.
	s.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	assert.False(t, s.VersionFocused())

	// pgdown/pgup/home/end all drop focus off the indicator too.
	for _, drop := range []tea.KeyPressMsg{
		{Code: tea.KeyPgDown}, {Code: tea.KeyPgUp}, {Code: tea.KeyHome}, {Code: tea.KeyEnd},
	} {
		s.cursor = 0
		s.Update(tea.KeyPressMsg{Code: 'k', Text: "k"})
		require.True(t, s.VersionFocused())
		s.Update(drop)
		assert.False(t, s.VersionFocused())
	}
}

// TestSidebar_HandleClick_ClearsVersionFocused covers HandleClick's new
// versionFocused=false reset ahead of the existing selection logic.
func TestSidebar_HandleClick_ClearsVersionFocused(t *testing.T) {
	tasks := makeTasks("a", "b")
	s := NewSidebar("RunWisp", "0.1.0", "", tasks)
	s.SetSize(20, 30)
	s.SetUpdate(true, "v0.2.0")
	s.FocusVersion()
	require.True(t, s.VersionFocused())

	s.HandleClick(s.brandHeight())
	assert.False(t, s.VersionFocused())
}

// TestSidebar_View_RendersUpdateIndicator exercises renderVersionLine's three
// branches: no update, update unfocused, and update focused.
func TestSidebar_View_RendersUpdateIndicator(t *testing.T) {
	s := NewSidebar("RunWisp", "0.1.0", "", makeTasks("a"))
	s.SetSize(30, 15)
	assert.NotContains(t, s.View(), "⚠")

	s.SetUpdate(true, "v0.2.0")
	assert.Contains(t, s.View(), "⚠")

	s.FocusVersion()
	out := s.View()
	assert.Contains(t, out, "⚠")
	assert.Contains(t, out, "v0.2.0")
}
