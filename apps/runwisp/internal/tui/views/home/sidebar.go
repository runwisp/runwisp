// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package home

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
)

type sidebarItem struct {
	label    string
	page     uikit.Page
	taskName string
	kind     entryKind
}

type entryKind int

const (
	entryPage entryKind = iota
	entryTask
	entryGroupHeader
)

// Sidebar renders the left panel: brand header + navigation.
//
// allItems is the canonical, unfiltered entry list; items is what is currently
// displayed. When the type-to-filter mode is inactive the two are identical.
// selected always indexes allItems (so the main panel keeps showing the active
// task even while the displayed list is narrowed), whereas cursor/scroll/hovered
// index the displayed items.
type Sidebar struct {
	name        string
	version     string
	fingerprint string
	allItems    []sidebarItem
	items       []sidebarItem

	width     int
	height    int
	cursor    int
	scroll    int
	selected  int
	hovered   int
	focused   bool
	filtering bool
	filter    string
}

func NewSidebar(name, version, fingerprint string, tasks []model.TaskBrief) Sidebar {
	all := buildItems(tasks)
	return Sidebar{
		name:        name,
		version:     version,
		fingerprint: fingerprint,
		allItems:    all,
		items:       all,
		hovered:     -1,
		focused:     true,
	}
}

// Rebuild replaces the entry list after a config reload, preserving the active
// selection by name/page when it still exists and exiting any filter sub-mode.
func (s *Sidebar) Rebuild(tasks []model.TaskBrief) {
	prevPage := s.ActivePage()
	prevTask := s.ActiveTask()
	s.allItems = buildItems(tasks)
	s.items = s.allItems
	s.filtering = false
	s.filter = ""
	s.hovered = -1
	s.selected = 0
	for i := range s.allItems {
		if s.allItems[i].page == prevPage && s.allItems[i].taskName == prevTask {
			s.selected = i
			break
		}
	}
	s.cursor = s.selected
	s.skipGroupHeaders(1)
	s.ensureVisible()
}

func buildItems(tasks []model.TaskBrief) []sidebarItem {
	items := []sidebarItem{
		{label: "Home", page: uikit.PageHome, kind: entryPage},
	}

	if hasMultipleGroups(tasks) {
		seen := make(map[string]bool)
		var groups []string
		for _, task := range tasks {
			if !seen[task.Group] {
				seen[task.Group] = true
				groups = append(groups, task.Group)
			}
		}
		items = append(items, buildGroupItems(tasks, groups)...)
	} else {
		for _, task := range tasks {
			items = append(items, sidebarItem{
				label:    task.Name,
				page:     uikit.PageHome,
				taskName: task.Name,
				kind:     entryTask,
			})
		}
	}

	items = append(items,
		sidebarItem{label: "Info", page: uikit.PageInfo, kind: entryPage},
		sidebarItem{label: "Debug", page: uikit.PageDebug, kind: entryPage},
	)
	return items
}

func buildGroupItems(tasks []model.TaskBrief, groups []string) []sidebarItem {
	var items []sidebarItem
	for _, group := range groups {
		items = append(items, sidebarItem{label: group, kind: entryGroupHeader})
		for _, task := range tasks {
			if task.Group != group {
				continue
			}
			items = append(items, sidebarItem{
				label:    task.Name,
				page:     uikit.PageHome,
				taskName: task.Name,
				kind:     entryTask,
			})
		}
	}
	return items
}

func hasMultipleGroups(tasks []model.TaskBrief) bool {
	if len(tasks) == 0 {
		return false
	}
	first := tasks[0].Group
	for _, t := range tasks[1:] {
		if t.Group != first {
			return true
		}
	}
	return false
}

// SetSize updates the sidebar dimensions.
func (s *Sidebar) SetSize(w, h int) {
	s.width = w
	s.height = h
	s.ensureVisible()
}

func (s *Sidebar) SetFocused(focused bool) {
	s.focused = focused
	if focused {
		s.ensureVisible()
	}
}

func (s *Sidebar) SetHovered(idx int) {
	s.hovered = idx
}

func (s *Sidebar) ActivePage() uikit.Page {
	if s.selected >= 0 && s.selected < len(s.allItems) {
		return s.allItems[s.selected].page
	}
	return uikit.PageHome
}

func (s *Sidebar) ActiveTask() string {
	if s.selected >= 0 && s.selected < len(s.allItems) {
		return s.allItems[s.selected].taskName
	}
	return ""
}

func (s *Sidebar) CursorTaskName() string {
	if s.cursor >= 0 && s.cursor < len(s.items) {
		return s.items[s.cursor].taskName
	}
	return ""
}

func (s *Sidebar) Update(msg tea.Msg) tea.Cmd {
	if !s.focused || len(s.items) == 0 {
		return nil
	}

	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil
	}

	switch keyMsg.String() {
	case "up", "k":
		s.MoveCursor(-1)
	case "down", "j":
		s.MoveCursor(1)
	case "pgup":
		s.PageCursor(-1)
	case "pgdown":
		s.PageCursor(1)
	case "home", "g":
		s.CursorToEdge(-1)
	case "end", "G":
		s.CursorToEdge(1)
	case "enter", "space":
		s.selectCursor()
	default:
		return nil
	}
	return nil
}

// MoveCursor moves the cursor by one selectable item in dir (+1 down, -1 up),
// skipping group headers.
func (s *Sidebar) MoveCursor(dir int) {
	last := len(s.items) - 1
	if dir < 0 && s.cursor > 0 {
		s.cursor--
	} else if dir > 0 && s.cursor < last {
		s.cursor++
	}
	s.skipGroupHeaders(dir)
	s.ensureVisible()
}

// PageCursor moves the cursor a viewport-height in dir, skipping group headers.
func (s *Sidebar) PageCursor(dir int) {
	last := len(s.items) - 1
	s.cursor += dir * s.visibleHeight()
	if s.cursor < 0 {
		s.cursor = 0
	}
	if s.cursor > last {
		s.cursor = last
	}
	// Nudge inward off a header: down-page skips forward, up-page skips back.
	if dir < 0 {
		s.skipGroupHeaders(1)
	} else {
		s.skipGroupHeaders(-1)
	}
	s.ensureVisible()
}

// CursorToEdge jumps the cursor to the first (dir<0) or last (dir>0) selectable
// item.
func (s *Sidebar) CursorToEdge(dir int) {
	if dir < 0 {
		s.cursor = 0
		s.skipGroupHeaders(1)
	} else {
		s.cursor = len(s.items) - 1
		s.skipGroupHeaders(-1)
	}
	s.ensureVisible()
}

// selectCursor activates the item under the cursor. selected is resolved against
// the canonical list so it stays valid even when the displayed list is filtered.
func (s *Sidebar) selectCursor() {
	if s.cursor < 0 || s.cursor >= len(s.items) {
		return
	}
	if idx := s.indexInAll(s.items[s.cursor]); idx >= 0 {
		s.selected = idx
	}
}

func (s *Sidebar) HandleClick(y int) {
	index := s.RowIndexAt(y)
	if index < 0 || s.items[index].kind == entryGroupHeader {
		return
	}
	s.cursor = index
	s.selectCursor()
	if s.filtering {
		s.StopFilter()
	}
	s.focused = true
	s.ensureVisible()
}

// Filtering reports whether the type-to-filter sub-mode is active.
func (s *Sidebar) Filtering() bool { return s.filtering }

// StartFilter enters the type-to-filter sub-mode with an empty query.
func (s *Sidebar) StartFilter() {
	s.filtering = true
	s.filter = ""
	s.cursor = 0
	s.applyFilter()
}

// StopFilter leaves the filter sub-mode and restores the full list, returning
// the cursor to the active selection.
func (s *Sidebar) StopFilter() {
	s.filtering = false
	s.filter = ""
	s.items = s.allItems
	s.cursor = s.selected
	s.skipGroupHeaders(1)
	s.ensureVisible()
}

// FilterAppend adds typed text to the query and re-narrows the list.
func (s *Sidebar) FilterAppend(text string) {
	s.filter += text
	s.applyFilter()
}

// FilterBackspace removes the last character from the query.
func (s *Sidebar) FilterBackspace() {
	if s.filter == "" {
		return
	}
	r := []rune(s.filter)
	s.filter = string(r[:len(r)-1])
	s.applyFilter()
}

// SelectFilterCursor activates the item under the cursor and exits filter mode.
func (s *Sidebar) SelectFilterCursor() {
	s.selectCursor()
	s.StopFilter()
}

// applyFilter recomputes the displayed list from the query. An empty query shows
// everything; otherwise only tasks whose name contains the query (case
// insensitive) remain.
func (s *Sidebar) applyFilter() {
	if s.filter == "" {
		s.items = s.allItems
	} else {
		q := strings.ToLower(s.filter)
		filtered := make([]sidebarItem, 0, len(s.allItems))
		for _, it := range s.allItems {
			if it.kind == entryTask && strings.Contains(strings.ToLower(it.taskName), q) {
				filtered = append(filtered, it)
			}
		}
		s.items = filtered
	}
	s.cursor = 0
	s.scroll = 0
	s.skipGroupHeaders(1)
	s.ensureVisible()
}

// indexInAll returns the canonical index of it, or -1 if not present.
func (s *Sidebar) indexInAll(it sidebarItem) int {
	for i := range s.allItems {
		if sameItem(s.allItems[i], it) {
			return i
		}
	}
	return -1
}

func sameItem(a, b sidebarItem) bool {
	return a.kind == b.kind && a.page == b.page && a.taskName == b.taskName && a.label == b.label
}

// skipGroupHeaders moves the cursor past any group header in the given
// direction (+1 or -1). If the cursor lands on a group header, it is
// nudged forward (or backward) until it reaches a selectable item.
func (s *Sidebar) skipGroupHeaders(dir int) {
	for s.cursor >= 0 && s.cursor < len(s.items) && s.items[s.cursor].kind == entryGroupHeader {
		s.cursor += dir
	}
	if s.cursor < 0 {
		s.cursor = 0
	}
	if s.cursor >= len(s.items) {
		s.cursor = len(s.items) - 1
	}
}

func (s *Sidebar) brandHeight() int {
	if s.fingerprint != "" {
		return 5
	}
	return 4
}

func (s *Sidebar) View() string {
	var b strings.Builder
	rendered := 0
	w := s.width

	writeSidebarLine(&b, "", w)
	rendered++
	writeSidebarLine(&b, uikit.SidebarMarkStyle.Render(" ⟡ ")+uikit.SidebarBrandStyle.Render(s.name), w)
	rendered++
	writeSidebarLine(&b, uikit.SidebarVersionStyle.Render("   v"+s.version), w)
	rendered++

	if s.fingerprint != "" {
		fingerprint := truncateToWidth(s.fingerprint, max(0, w-3))
		writeSidebarLine(&b, uikit.SidebarFingerprintStyle.Render("   "+fingerprint), w)
		rendered++
	}

	writeSidebarLine(&b, "", w)
	rendered++

	if s.filtering {
		writeSidebarLine(&b, s.renderFilterLine(w), w)
		rendered++
	}

	start := s.scroll
	end := start + s.visibleHeight()
	if end > len(s.items) {
		end = len(s.items)
	}
	for i := start; i < end; i++ {
		writeSidebarLine(&b, s.renderItem(i), w)
		rendered++
	}

	for rendered < s.height {
		writeSidebarLine(&b, "", w)
		rendered++
	}

	return b.String()
}

func (s *Sidebar) renderItem(index int) string {
	item := s.items[index]

	if item.kind == entryGroupHeader {
		text := " " + item.label
		if width := lipgloss.Width(text); width < s.width {
			text += strings.Repeat(" ", s.width-width)
		}
		return uikit.SidebarGroupHeaderStyle.Render(text)
	}

	label := item.label
	if item.kind == entryTask {
		label = "  " + label
	}

	selected := s.isSelected(index)
	indicator := "  "
	if selected {
		indicator = "▸ "
	}

	text := " " + indicator + truncateToWidth(label, max(1, s.width-4))
	if width := lipgloss.Width(text); width < s.width {
		text += strings.Repeat(" ", s.width-width)
	}

	style := uikit.SidebarItemNoneStyle
	switch {
	case s.focused && index == s.cursor && selected:
		style = uikit.SidebarItemFocusedCursorSelectedStyle
	case s.focused && index == s.cursor:
		style = uikit.SidebarItemFocusedCursorStyle
	case s.focused && selected:
		style = uikit.SidebarItemFocusedSelectedStyle
	case selected:
		style = uikit.SidebarItemSelectedStyle
	case index == s.hovered:
		style = uikit.SidebarItemHoveredStyle
	case s.focused:
		style = uikit.SidebarItemFocusedStyle
	}

	return style.Render(text)
}

// isSelected reports whether the displayed item at displayedIdx is the active
// selection. selected indexes the canonical list, so the match is by identity.
func (s *Sidebar) isSelected(displayedIdx int) bool {
	if s.selected < 0 || s.selected >= len(s.allItems) {
		return false
	}
	if displayedIdx < 0 || displayedIdx >= len(s.items) {
		return false
	}
	return sameItem(s.items[displayedIdx], s.allItems[s.selected])
}

// filterHeaderHeight is the number of list rows consumed by the filter prompt.
func (s *Sidebar) filterHeaderHeight() int {
	if s.filtering {
		return 1
	}
	return 0
}

// renderFilterLine draws the type-to-filter prompt shown above the item list.
func (s *Sidebar) renderFilterLine(w int) string {
	text := truncateToWidth(" / "+s.filter+"▏", w)
	return lipgloss.NewStyle().
		Background(uikit.ColorSidebarBg).
		Foreground(uikit.ColorPrimary).
		Render(text)
}

func (s *Sidebar) visibleHeight() int {
	listHeight := s.height - s.brandHeight() - s.filterHeaderHeight()
	if listHeight < 1 {
		return 1
	}
	return listHeight
}

func (s *Sidebar) RowIndexAt(y int) int {
	localY := y - s.brandHeight() - s.filterHeaderHeight()
	if localY < 0 || localY >= s.visibleHeight() {
		return -1
	}
	index := s.scroll + localY
	if index < 0 || index >= len(s.items) {
		return -1
	}
	return index
}

func (s *Sidebar) ensureVisible() {
	if len(s.items) == 0 {
		s.cursor = 0
		s.scroll = 0
		return
	}

	if s.cursor < 0 {
		s.cursor = 0
	}
	if s.cursor >= len(s.items) {
		s.cursor = len(s.items) - 1
	}

	visibleHeight := s.visibleHeight()
	if s.cursor < s.scroll {
		s.scroll = s.cursor
	} else if s.cursor >= s.scroll+visibleHeight {
		s.scroll = s.cursor - visibleHeight + 1
	}

	maxScroll := len(s.items) - visibleHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if s.scroll > maxScroll {
		s.scroll = maxScroll
	}
	if s.scroll < 0 {
		s.scroll = 0
	}
}

func writeSidebarLine(b *strings.Builder, content string, width int) {
	b.WriteString(uikit.PadLine(content, width, uikit.ColorSidebarBg))
	b.WriteString("\n")
}

// truncateToWidth clips to maxWidth display columns with an ellipsis, but leaves
// the value untouched when maxWidth <= 0 (no budget declared → don't clip).
func truncateToWidth(value string, maxWidth int) string {
	if maxWidth <= 0 {
		return value
	}
	return uikit.TruncateToWidth(value, maxWidth)
}
