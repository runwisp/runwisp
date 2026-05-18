// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package home

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeTasks(names ...string) []model.TaskBrief {
	tasks := make([]model.TaskBrief, len(names))
	for i, n := range names {
		tasks[i] = model.TaskBrief{Name: n}
	}
	return tasks
}

func makeGroupedTasks() []model.TaskBrief {
	return []model.TaskBrief{
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

	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	assert.Equal(t, 1, s.cursor)

	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	assert.Equal(t, 0, s.cursor)

	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	assert.Greater(t, s.cursor, 0)

	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	assert.Equal(t, 0, s.cursor)

	s.Update(tea.KeyMsg{Type: tea.KeyEnd})
	lastCursor := s.cursor

	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	assert.Equal(t, lastCursor, s.selected)
}

func TestSidebar_Update_NotFocused_IgnoresInput(t *testing.T) {
	tasks := makeTasks("t1")
	s := NewSidebar("RunWisp", "0.1.0", "", tasks)
	s.SetFocused(false)
	s.cursor = 0

	s.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
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
