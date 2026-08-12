// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/runwisp/runwisp/internal/tui/keys"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/execlist"
	"github.com/runwisp/runwisp/internal/tui/views/home"
)

func (m Model) View() tea.View {
	var v tea.View
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	if m.dialogs.MouseDisabled() {
		v.MouseMode = tea.MouseModeNone
	}

	if !m.ready {
		v.SetContent("Initializing...")
		return v
	}

	// During a coalesced mouse-motion/wheel burst, reuse the last full frame
	// instead of rebuilding the whole screen for every event (bug 2).
	if m.coalesce && m.frame != nil && *m.frame != "" {
		v.SetContent(*m.frame)
		return v
	}

	body := m.renderBody()
	output := body + "\n" + m.renderHelpBar()
	if m.logSearch != nil {
		output = m.logSearch.View(m.width, m.height)
	}
	content := m.dialogs.RenderOverlays(output, m.width, m.height)
	if m.frame != nil {
		*m.frame = content
	}
	v.SetContent(content)
	return v
}

// renderHelpBar builds the bottom help bar from fully-styled segments that each
// carry the bar background, then fills the line width with PadLine. Styling each
// segment (rather than wrapping a pre-rendered, foreground-only flash in one
// width-bounded Render) keeps the background continuous: an embedded reset is
// always immediately followed by the next segment's full SGR, so no part of the
// line falls back to the terminal's default background.
func (m Model) renderHelpBar() string {
	bar := uikit.HelpBarStyle.Render(m.buildHelpText())
	if flash, ok := m.dialogs.FlashActive(); ok {
		flashStr := lipgloss.NewStyle().
			Background(uikit.ColorBgLight).
			Foreground(uikit.ColorSuccess).
			Bold(true).
			PaddingLeft(1).
			Render(flash)
		bar = flashStr + bar
	}
	return uikit.PadLine(bar, m.width, uikit.ColorBgLight)
}

func (m Model) renderBody() string {
	if m.isExecFullscreen() {
		return strings.TrimRight(m.execView.View(), "\n")
	}
	return lipgloss.JoinHorizontal(lipgloss.Top,
		strings.TrimRight(m.sidebar.View(), "\n"),
		strings.TrimRight(m.renderMainContent(), "\n"),
	)
}

func (m Model) renderMainContent() string {
	if m.execView != nil {
		return m.execView.View()
	}
	mainW, _ := m.mainSize()
	panelW := m.contentWidth()
	m.notifications.SetWidth(panelW)
	panelView := ""
	if m.sidebar.ActivePage() == uikit.PageHome && m.notifications.PanelHeight() > 0 {
		panelView = m.notifications.View() + "\n"
	}
	switch m.sidebar.ActivePage() {
	case uikit.PageHome:
		// Render the panel at the bounded width, then fill the right margin with
		// the app background so a wide terminal shows a left-aligned panel.
		return padPanelRight(m.renderHomeContent(panelW, panelView), mainW)
	case uikit.PageInfo:
		return m.infoView.View()
	case uikit.PageDebug:
		return m.debugView.View()
	}
	return ""
}

func (m Model) renderHomeContent(panelW int, panelView string) string {
	if m.sidebar.ActiveTask() != "" {
		runNowHovered := m.mouse.hoverY == m.layout.taskBtnY && m.mouse.hoverX >= uikit.SidebarWidth
		header, _ := home.RenderTaskHeader(m.sidebar.ActiveTask(), m.taskDisplayByName(m.sidebar.ActiveTask()), panelW, runNowHovered)
		return header + panelView + m.execList.View()
	}
	header, _ := home.RenderHeader(m.info, m.hasLaunchTicket(), panelW, m.homeCursor, m.mouse.homeHover)
	return header + panelView + m.execList.View()
}

// padPanelRight right-pads every line of a bounded panel block to width with the
// app background, leaving the panel left-aligned and the margin filled. PadLine
// is a no-op for lines already at width, so narrow terminals are unaffected.
func padPanelRight(block string, width int) string {
	lines := strings.Split(strings.TrimRight(block, "\n"), "\n")
	for i, ln := range lines {
		lines[i] = uikit.PadLine(ln, width, uikit.ColorBg)
	}
	return strings.Join(lines, "\n")
}

func (m Model) buildHelpText() string {
	return m.buildContextHelpText() + "  " + keys.Help.Bar
}

func (m Model) buildContextHelpText() string {
	if m.sidebar.Filtering() {
		return "type to filter  ↑↓ move  enter select  esc cancel"
	}
	if m.notifications.IsExpanded() {
		return keys.JoinBar(keys.Move, keys.NotifOpen, keys.NotifRead, keys.NotifReadAll, keys.NotifCollapse, keys.Quit)
	}
	if m.runListFocused() && m.execList.SelectionActive() {
		return m.buildSelectionHelpText()
	}
	if m.execView != nil {
		return m.buildExecViewHelpText()
	}
	if m.panelFocus == uikit.PanelSidebar {
		return m.buildSidebarHelpText()
	}
	return m.buildMainHelpText()
}

func (m Model) buildExecViewHelpText() string {
	var parts []string
	if m.execView.Fullscreen() {
		scroll := []keys.Binding{keys.ExitFull, keys.Scroll}
		if m.execView.MaxHScroll() > 0 {
			scroll = append(scroll, keys.Pan)
		}
		scroll = append(scroll, keys.LogJump)
		return strings.Join([]string{keys.JoinBar(scroll...), "select text with mouse", keys.Quit.Bar}, "  ")
	}
	switch m.execView.HeaderFocus {
	case execlist.HeaderFocusBack, execlist.HeaderFocusAction:
		parts = append(parts, "enter activate  ←→ switch  ↓ details")
	case execlist.HeaderFocusID:
		parts = append(parts, "enter copy  ←→ switch  ↓ details")
	case execlist.HeaderFocusStarted, execlist.HeaderFocusDuration:
		parts = append(parts, "enter copy  ←→ switch  ↑ buttons  ↓ log")
	default:
		scroll := []keys.Binding{keys.BackToList, keys.Scroll}
		if m.execView.MaxHScroll() > 0 {
			scroll = append(scroll, keys.Pan)
		}
		scroll = append(scroll, keys.LogJump, keys.Fullscreen)
		parts = append(parts, keys.JoinBar(scroll...))
	}
	parts = m.appendExecViewActionHints(parts)
	parts = append(parts, keys.Quit.Bar)
	return strings.Join(parts, "  ")
}

func (m Model) appendExecViewActionHints(parts []string) []string {
	if m.execView.Run == nil {
		return parts
	}
	parts = append(parts, keys.TaskInfo.Bar)
	switch m.execView.Action() {
	case execlist.ActionStop:
		parts = append(parts, "s stop")
	case execlist.ActionStopService:
		parts = append(parts, "s stop service")
	case execlist.ActionRetry:
		parts = append(parts, "r retry")
	case execlist.ActionRestartService:
		parts = append(parts, keys.Restart.Bar)
	}
	if m.hasLaunchTicket() {
		parts = append(parts, "d download")
	}
	if m.execView.CanDelete() {
		parts = append(parts, "D delete")
	}
	return parts
}

func (m Model) buildSidebarHelpText() string {
	if name := m.sidebar.CursorTaskName(); name != "" {
		actionHint := keys.RunNow.Bar
		if m.isService(name) {
			actionHint = keys.Restart.Bar
		}
		return keys.Move.Bar + "  enter select  " + actionHint + "  " + keys.TaskInfo.Bar + "  " + keys.FilterTasks.Bar + "  → main panel  " + keys.Quit.Bar
	}
	return keys.Move.Bar + "  enter select  " + keys.FilterTasks.Bar + "  → main panel  " + keys.Quit.Bar
}

func (m Model) buildMainHelpText() string {
	if m.homeCursor >= 0 {
		fields := home.Fields(m.info, m.hasLaunchTicket())
		if m.homeCursor < len(fields) && fields[m.homeCursor] == home.FieldOpenWebUI {
			return keys.JoinBar(keys.Move, keys.Open, keys.ToSidebar, keys.Quit)
		}
		return keys.Move.Bar + "  enter copy  " + keys.ToSidebar.Bar + "  " + keys.Quit.Bar
	}
	if m.sidebar.ActivePage() == uikit.PageInfo {
		return keys.JoinBar(keys.Scroll, keys.BackSidebar, keys.Quit)
	}
	if m.sidebar.ActivePage() == uikit.PageDebug {
		return keys.JoinBar(keys.Scroll, keys.LogJump, keys.BackSidebar, keys.Quit)
	}
	if name := m.sidebar.ActiveTask(); name != "" {
		actionHint := keys.RunNow.Bar
		if m.isService(name) {
			actionHint = keys.Restart.Bar
		}
		base := keys.JoinBar(keys.Move, keys.Open) + "  " + actionHint + "  " +
			keys.JoinBar(keys.Filter, keys.TaskInfo) + "  " + keys.ToSidebar.Bar
		if m.sidebar.ActivePage() == uikit.PageHome && m.notifications.PanelHeight() > 0 {
			base += "  " + keys.NotifPanel.Bar
		}
		return base + "  " + keys.Quit.Bar
	}
	base := keys.JoinBar(keys.Move, keys.Open, keys.Filter, keys.Select, keys.ToSidebar)
	if m.sidebar.ActivePage() == uikit.PageHome && m.notifications.PanelHeight() > 0 {
		base += "  " + keys.NotifPanel.Bar
	}
	return base + "  " + keys.Quit.Bar
}

// buildSelectionHelpText renders the bottom-bar action set shown while one or
// more runs are multi-selected in the executions list.
func (m Model) buildSelectionHelpText() string {
	count := fmt.Sprintf("%d selected", m.execList.SelectionCount())
	return count + "  " + keys.JoinBar(keys.BulkDelete, keys.BulkCancel, keys.BulkRerun, keys.SelectAll, keys.ClearSelect)
}
