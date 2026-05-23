// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/execlist"
	"github.com/runwisp/runwisp/internal/tui/views/home"
)

func (m Model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	body := m.renderBody()
	helpBar := uikit.HelpBarStyle.Width(m.width).Render(m.helpTextWithFlash())
	output := body + "\n" + helpBar
	if m.logSearch != nil {
		output = m.logSearch.View(m.width, m.height)
	}
	return m.dialogs.RenderOverlays(output, m.width, m.height)
}

func (m Model) helpTextWithFlash() string {
	helpText := m.buildHelpText()
	flash, ok := m.dialogs.FlashActive()
	if !ok {
		return helpText
	}
	flashStr := lipgloss.NewStyle().Foreground(uikit.ColorSuccess).Bold(true).Render(flash)
	return flashStr + "  " + helpText
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
	m.notifications.SetWidth(mainW)
	panelView := ""
	if m.sidebar.ActivePage() == uikit.PageHome && m.notifications.PanelHeight() > 0 {
		panelView = m.notifications.View() + "\n"
	}
	switch m.sidebar.ActivePage() {
	case uikit.PageHome:
		return m.renderHomeContent(mainW, panelView)
	case uikit.PageInfo:
		return m.infoView.View()
	case uikit.PageDebug:
		return m.debugView.View()
	}
	return ""
}

func (m Model) renderHomeContent(mainW int, panelView string) string {
	if m.sidebar.ActiveTask() != "" {
		runNowHovered := m.mouse.hoverY == m.layout.taskBtnY && m.mouse.hoverX >= uikit.SidebarWidth
		header, _ := home.RenderTaskHeader(m.sidebar.ActiveTask(), m.taskDisplayByName(m.sidebar.ActiveTask()), mainW, runNowHovered)
		return header + panelView + m.execList.View()
	}
	header, _ := home.RenderHeader(m.info, m.hasLaunchTicket(), mainW, m.homeCursor, m.mouse.homeHover)
	return header + panelView + m.execList.View()
}

func (m Model) buildHelpText() string {
	if m.notifications.IsExpanded() {
		return "↑/↓ navigate  enter open  r mark read  n/esc collapse  q/^C quit"
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
		scrollParts := "esc/f exit fullscreen  ↑↓ scroll"
		if m.execView.MaxHScroll() > 0 {
			scrollParts += "  ←→ pan"
		}
		scrollParts += "  G end  g top  pgup/pgdn page"
		return strings.Join([]string{scrollParts, "select text with mouse", "q/^C quit"}, "  ")
	}
	switch m.execView.HeaderFocus {
	case execlist.HeaderFocusBack, execlist.HeaderFocusAction:
		parts = append(parts, "enter activate  ←→ switch  ↓ details")
	case execlist.HeaderFocusID:
		parts = append(parts, "enter copy  ←→ switch  ↓ details")
	case execlist.HeaderFocusStarted, execlist.HeaderFocusDuration:
		parts = append(parts, "enter copy  ←→ switch  ↑ buttons  ↓ log")
	default:
		scrollParts := "esc/⌫ back  ↑↓ scroll"
		if m.execView.MaxHScroll() > 0 {
			scrollParts += "  ←→ pan"
		}
		scrollParts += "  G end  g top  pgup/pgdn page  f fullscreen"
		parts = append(parts, scrollParts)
	}
	parts = m.appendExecViewActionHints(parts)
	parts = append(parts, "q/^C quit")
	return strings.Join(parts, "  ")
}

func (m Model) appendExecViewActionHints(parts []string) []string {
	if m.execView.Run == nil {
		return parts
	}
	switch m.execView.Action() {
	case execlist.ActionStop:
		parts = append(parts, "s stop")
	case execlist.ActionStopService:
		parts = append(parts, "s stop service")
	case execlist.ActionRetry:
		parts = append(parts, "r retry")
	case execlist.ActionRestartService:
		parts = append(parts, "r restart")
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
		actionHint := "r run now"
		if m.isService(name) {
			actionHint = "r restart"
		}
		return "↑↓ navigate  enter select  " + actionHint + "  → main panel  q/^C quit"
	}
	return "↑↓ navigate  enter select  → main panel  q/^C quit"
}

func (m Model) buildMainHelpText() string {
	if m.homeCursor >= 0 {
		fields := home.Fields(m.info, m.hasLaunchTicket())
		if m.homeCursor < len(fields) && fields[m.homeCursor] == home.FieldOpenWebUI {
			return "↑↓ navigate  enter open  esc/← sidebar  q/^C quit"
		}
		return "↑↓ navigate  enter copy  esc/← sidebar  q/^C quit"
	}
	if m.sidebar.ActivePage() == uikit.PageInfo {
		return "↑↓ scroll  ← sidebar  q/^C quit"
	}
	if m.sidebar.ActivePage() == uikit.PageDebug {
		return "↑↓ scroll  G end  g top  pgup/pgdn page  ← sidebar  q/^C quit"
	}
	if name := m.sidebar.ActiveTask(); name != "" {
		actionHint := "r run now"
		if m.isService(name) {
			actionHint = "r restart"
		}
		base := "↑↓ navigate  enter open  " + actionHint + "  esc/← sidebar"
		if m.sidebar.ActivePage() == uikit.PageHome && m.notifications.PanelHeight() > 0 {
			base += "  n notifications"
		}
		return base + "  q/^C quit"
	}
	base := "↑↓ navigate  enter open  esc/← sidebar"
	if m.sidebar.ActivePage() == uikit.PageHome && m.notifications.PanelHeight() > 0 {
		base += "  n notifications"
	}
	return base + "  q/^C quit"
}
