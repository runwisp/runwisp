// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package home

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
type Sidebar struct {
	name        string
	version     string
	fingerprint string
	items       []sidebarItem

	width    int
	height   int
	cursor   int
	scroll   int
	selected int
	hovered  int
	focused  bool
}

func NewSidebar(name, version, fingerprint string, tasks []model.TaskBrief) Sidebar {
	return Sidebar{
		name:        name,
		version:     version,
		fingerprint: fingerprint,
		items:       buildItems(tasks),
		hovered:     -1,
		focused:     true,
	}
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
	if s.selected >= 0 && s.selected < len(s.items) {
		return s.items[s.selected].page
	}
	return uikit.PageHome
}

func (s *Sidebar) ActiveTask() string {
	if s.selected >= 0 && s.selected < len(s.items) {
		return s.items[s.selected].taskName
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

	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}

	last := len(s.items) - 1
	switch keyMsg.String() {
	case "up", "k":
		if s.cursor > 0 {
			s.cursor--
			s.skipGroupHeaders(-1)
		}
	case "down", "j":
		if s.cursor < last {
			s.cursor++
			s.skipGroupHeaders(1)
		}
	case "pgup":
		s.cursor -= s.visibleHeight()
		if s.cursor < 0 {
			s.cursor = 0
		}
		s.skipGroupHeaders(1)
	case "pgdown":
		s.cursor += s.visibleHeight()
		if s.cursor > last {
			s.cursor = last
		}
		s.skipGroupHeaders(-1)
	case "home", "g":
		s.cursor = 0
		s.skipGroupHeaders(1)
	case "end", "G":
		s.cursor = last
		s.skipGroupHeaders(-1)
	case "enter", " ":
		s.selected = s.cursor
	default:
		return nil
	}

	s.ensureVisible()
	return nil
}

func (s *Sidebar) HandleClick(y int) {
	index := s.RowIndexAt(y)
	if index < 0 || s.items[index].kind == entryGroupHeader {
		return
	}
	s.cursor = index
	s.selected = index
	s.focused = true
	s.ensureVisible()
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

	indicator := "  "
	if index == s.selected {
		indicator = "▸ "
	}

	text := " " + indicator + truncateToWidth(label, max(1, s.width-4))
	if width := lipgloss.Width(text); width < s.width {
		text += strings.Repeat(" ", s.width-width)
	}

	style := uikit.SidebarItemNoneStyle
	switch {
	case s.focused && index == s.cursor && index == s.selected:
		style = uikit.SidebarItemFocusedCursorSelectedStyle
	case s.focused && index == s.cursor:
		style = uikit.SidebarItemFocusedCursorStyle
	case s.focused && index == s.selected:
		style = uikit.SidebarItemFocusedSelectedStyle
	case index == s.selected:
		style = uikit.SidebarItemSelectedStyle
	case index == s.hovered:
		style = uikit.SidebarItemHoveredStyle
	case s.focused:
		style = uikit.SidebarItemFocusedStyle
	}

	return style.Render(text)
}

func (s *Sidebar) visibleHeight() int {
	listHeight := s.height - s.brandHeight()
	if listHeight < 1 {
		return 1
	}
	return listHeight
}

func (s *Sidebar) RowIndexAt(y int) int {
	localY := y - s.brandHeight()
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

func truncateToWidth(value string, maxWidth int) string {
	if maxWidth <= 0 || lipgloss.Width(value) <= maxWidth {
		return value
	}
	if maxWidth == 1 {
		return "…"
	}

	runes := []rune(value)
	for len(runes) > 0 {
		runes = runes[:len(runes)-1]
		if lipgloss.Width(string(runes))+1 <= maxWidth {
			return string(runes) + "…"
		}
	}

	return "…"
}
