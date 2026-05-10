// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	var body string
	if m.isExecFullscreen() {
		body = strings.TrimRight(m.execView.View(), "\n")
	} else {
		sidebarView := m.sidebar.View()

		var mainContent string
		if m.execView != nil {
			mainContent = m.execView.View()
		} else {
			mainW, _ := m.mainSize()
			m.notifications.SetWidth(mainW)
			panelView := ""
			if m.sidebar.ActivePage() == PageHome && m.notifications.PanelHeight() > 0 {
				panelView = m.notifications.View() + "\n"
			}
			switch m.sidebar.ActivePage() {
			case PageHome:
				if m.sidebar.ActiveTask() != "" {
					runNowHovered := m.mouse.hoverY == m.layout.taskBtnY && m.mouse.hoverX >= sidebarWidth
					header, _ := renderTaskHeader(m.sidebar.ActiveTask(), m.taskDisplayByName(m.sidebar.ActiveTask()), mainW, runNowHovered)
					mainContent = header + panelView + m.execList.View()
				} else {
					header, _ := renderHomeHeader(m.info, m.hasLaunchTicket(), mainW, m.homeCursor, m.mouse.homeHover)
					mainContent = header + panelView + m.execList.View()
				}
			case PageInfo:
				mainContent = m.infoView.View()
			case PageDebug:
				mainContent = m.debugView.View()
			}
		}

		body = lipgloss.JoinHorizontal(lipgloss.Top,
			strings.TrimRight(sidebarView, "\n"),
			strings.TrimRight(mainContent, "\n"),
		)
	}

	helpText := m.buildHelpText()
	if flash, ok := m.dialogs.FlashActive(); ok {
		flashStr := lipgloss.NewStyle().Foreground(colorSuccess).Bold(true).Render(flash)
		helpText = flashStr + "  " + helpText
	}
	helpBar := helpBarStyle.Width(m.width).Render(helpText)

	output := body + "\n" + helpBar
	output = m.dialogs.RenderOverlays(output, m.width, m.height)

	return output
}

func (m Model) buildHelpText() string {
	if m.notifications.IsExpanded() {
		return "↑/↓ navigate  enter open  r mark read  n/esc collapse  q/^C quit"
	}
	if m.execView != nil {
		var parts []string
		if m.execView.Fullscreen() {
			scrollParts := "esc/f exit fullscreen  ↑↓ scroll"
			if m.execView.maxHScroll() > 0 {
				scrollParts += "  ←→ pan"
			}
			scrollParts += "  G end  g top  pgup/pgdn page"
			parts = append(parts, scrollParts, "select text with mouse", "q/^C quit")
			return strings.Join(parts, "  ")
		}
		switch m.execView.headerFocus {
		case headerFocusBack, headerFocusAction:
			parts = append(parts, "enter activate  ←→ switch  ↓ details")
		case headerFocusID:
			parts = append(parts, "enter copy  ←→ switch  ↓ details")
		case headerFocusStarted, headerFocusDuration:
			parts = append(parts, "enter copy  ←→ switch  ↑ buttons  ↓ log")
		default:
			scrollParts := "esc/⌫ back  ↑↓ scroll"
			if m.execView.maxHScroll() > 0 {
				scrollParts += "  ←→ pan"
			}
			scrollParts += "  G end  g top  pgup/pgdn page  f fullscreen"
			parts = append(parts, scrollParts)
		}
		if m.execView.run != nil {
			switch m.execView.Action() {
			case execViewActionStop:
				parts = append(parts, "s stop")
			case execViewActionRetry:
				parts = append(parts, "r retry")
			}
			if m.hasLaunchTicket() {
				parts = append(parts, "d download")
			}
		}
		parts = append(parts, "q/^C quit")
		return strings.Join(parts, "  ")
	}
	if m.panelFocus == PanelSidebar {
		if name := m.sidebar.CursorTaskName(); name != "" {
			actionHint := "r run now"
			if m.isService(name) {
				actionHint = "r restart"
			}
			return "↑↓ navigate  enter select  " + actionHint + "  → main panel  q/^C quit"
		}
		return "↑↓ navigate  enter select  → main panel  q/^C quit"
	}
	if m.homeCursor >= 0 {
		fields := homeFields(m.info, m.hasLaunchTicket())
		if m.homeCursor < len(fields) && fields[m.homeCursor] == homeFieldOpenWebUI {
			return "↑↓ navigate  enter open  esc/← sidebar  q/^C quit"
		}
		return "↑↓ navigate  enter copy  esc/← sidebar  q/^C quit"
	}
	if m.sidebar.ActivePage() == PageInfo {
		return "↑↓ scroll  ← sidebar  q/^C quit"
	}
	if m.sidebar.ActivePage() == PageDebug {
		return "↑↓ scroll  G end  g top  pgup/pgdn page  ← sidebar  q/^C quit"
	}
	if name := m.sidebar.ActiveTask(); name != "" {
		actionHint := "r run now"
		if m.isService(name) {
			actionHint = "r restart"
		}
		base := "↑↓ navigate  enter open  " + actionHint + "  esc/← sidebar"
		if m.sidebar.ActivePage() == PageHome && m.notifications.PanelHeight() > 0 {
			base += "  n notifications"
		}
		return base + "  q/^C quit"
	}
	base := "↑↓ navigate  enter open  esc/← sidebar"
	if m.sidebar.ActivePage() == PageHome && m.notifications.PanelHeight() > 0 {
		base += "  n notifications"
	}
	return base + "  q/^C quit"
}
