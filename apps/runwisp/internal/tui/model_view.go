// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/keys"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/views/execlist"
	"github.com/runwisp/runwisp/apps/runwisp/internal/tui/views/home"
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
	// instead of rebuilding the whole screen for every event.
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
	// The toast floats bottom-right, just above the help bar, on top of any
	// dialog so feedback like "Copied" stays visible while a modal is open.
	if toast, ok := m.renderToast(); ok {
		x := m.width - lipgloss.Width(toast) - 1
		y := m.height - 1 - lipgloss.Height(toast)
		content = uikit.OverlayAt(content, toast, x, y)
	}
	if m.frame != nil {
		*m.frame = content
	}
	v.SetContent(content)
	return v
}

// renderHelpBar builds the bottom help bar, dropping low-priority hints until
// it fits the terminal, and fills the line width with the bar background.
func (m Model) renderHelpBar() string {
	text := m.buildHelpText().fit(m.width - uikit.HelpBarStyle.GetHorizontalFrameSize())
	return uikit.PadLine(uikit.HelpBarStyle.Render(text), m.width, uikit.ColorBgLight)
}

// toastMaxWidth caps the toast box so long error messages wrap instead of
// covering the whole bottom of the screen.
const toastMaxWidth = 60

// renderToast renders the active flash as a bordered box, green for success
// and red for failures, with a `u undo` hint when the toast is undoable.
func (m Model) renderToast() (string, bool) {
	msg, ok := m.dialogs.FlashActive()
	if !ok {
		return "", false
	}
	accent, icon := uikit.ColorSuccess, "✓ "
	if m.dialogs.FlashIsError() {
		accent, icon = uikit.ColorError, "✗ "
	}
	bg := lipgloss.NewStyle().Background(uikit.ColorBgLight)
	body := bg.Foreground(accent).Bold(true).Render(icon) + bg.Foreground(uikit.ColorTextBright).Render(msg)
	if m.dialogs.HasUndo() {
		body += "\n" + bg.Foreground(uikit.ColorTextMuted).Render("u undo")
	}
	// Inner width: text plus 1-cell padding each side, bounded by the cap and
	// the terminal (minus border and a 1-cell right margin).
	inner := min(lipgloss.Width(body)+2, toastMaxWidth, m.width-3)
	if inner < 4 {
		return "", false
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accent).
		BorderBackground(uikit.ColorBgLight).
		Background(uikit.ColorBgLight).
		Padding(0, 1).
		Width(inner + 2).
		Render(body), true
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
		h := m.taskHeader(m.sidebar.ActiveTask())
		if m.mouse.hoverY == m.layout.taskBtnY {
			h.Hovered = h.ButtonAt(m.mouse.hoverX-uikit.SidebarWidth, panelW)
		}
		header, _ := h.Render(panelW)
		return header + panelView + m.execList.View()
	}
	starHovered := m.mouse.hoverY == home.StarButtonY && home.StarButtonAt(m.mouse.hoverX-uikit.SidebarWidth, panelW)
	header, _ := home.RenderHeader(m.info, m.hasLaunchTicket(), panelW, m.homeCursor, m.mouse.homeHover, starHovered)
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

func (m Model) buildHelpText() helpBar {
	return m.buildContextHelpText().add(prioHelp, keys.Help.Bar)
}

func (m Model) buildContextHelpText() helpBar {
	if m.sidebar.Filtering() {
		return helpBar{}.add(prioAction, "type to filter").add(prioNav, "↑↓ move", "enter select", "esc cancel")
	}
	if m.notifications.IsExpanded() {
		return helpBar{}.add(prioNav, keys.Move.Bar).
			add(prioAction, keys.NotifOpen.Bar, keys.NotifRead.Bar, keys.NotifReadAll.Bar).
			add(prioNav, keys.NotifCollapse.Bar).
			add(prioQuit, keys.Quit.Bar)
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

func (m Model) buildExecViewHelpText() helpBar {
	var bar helpBar
	if m.execView.Fullscreen() {
		bar = bar.add(prioNav, keys.ExitFull.Bar, keys.Scroll.Bar)
		if m.execView.MaxHScroll() > 0 {
			bar = bar.add(prioNav, keys.Pan.Bar)
		}
		return bar.add(prioNav, keys.LogJump.Bar, "select text with mouse").add(prioQuit, keys.Quit.Bar)
	}
	switch m.execView.HeaderFocus {
	case execlist.HeaderFocusBack, execlist.HeaderFocusAction, execlist.HeaderFocusDelete:
		bar = bar.add(prioAction, "enter activate").add(prioNav, "←→ switch", "↓ details")
	case execlist.HeaderFocusID:
		bar = bar.add(prioAction, "enter copy").add(prioNav, "←→ switch", "↓ details")
	case execlist.HeaderFocusStarted, execlist.HeaderFocusDuration:
		bar = bar.add(prioAction, "enter copy").add(prioNav, "←→ switch", "↑ buttons", "↓ log")
	default:
		bar = bar.add(prioNav, keys.BackToList.Bar, keys.Scroll.Bar)
		if m.execView.MaxHScroll() > 0 {
			bar = bar.add(prioNav, keys.Pan.Bar)
		}
		bar = bar.add(prioNav, keys.LogJump.Bar, keys.Fullscreen.Bar)
	}
	return m.appendExecViewActionHints(bar).add(prioQuit, keys.Quit.Bar)
}

func (m Model) appendExecViewActionHints(bar helpBar) helpBar {
	if m.execView.Run == nil {
		return bar
	}
	bar = bar.add(prioAction, keys.TaskInfo.Bar)
	switch m.execView.Action() {
	case execlist.ActionStop:
		bar = bar.add(prioAction, keys.Stop.Bar)
	case execlist.ActionStopService:
		bar = bar.add(prioAction, "s stop service")
	case execlist.ActionRetry:
		bar = bar.add(prioAction, "r retry")
	case execlist.ActionRestartService:
		bar = bar.add(prioAction, keys.Restart.Bar)
	}
	if m.hasLaunchTicket() {
		bar = bar.add(prioAction, "d download")
	}
	if m.execView.CanDelete() {
		bar = bar.add(prioAction, "D delete")
	}
	return bar
}

func (m Model) buildSidebarHelpText() helpBar {
	bar := helpBar{}.add(prioNav, keys.Move.Bar, "enter select")
	if name := m.sidebar.CursorTaskName(); name != "" {
		bar = bar.add(prioAction, m.taskHeader(name).Hints()...).add(prioAction, keys.TaskInfo.Bar)
	}
	return bar.add(prioNav, keys.FilterTasks.Bar, "→ main panel").add(prioQuit, keys.Quit.Bar)
}

func (m Model) buildMainHelpText() helpBar {
	if m.homeCursor >= 0 {
		enter := "enter copy"
		fields := home.Fields(m.info, m.hasLaunchTicket())
		if m.homeCursor < len(fields) && fields[m.homeCursor] == home.FieldOpenWebUI {
			enter = keys.Open.Bar
		}
		return helpBar{}.add(prioNav, keys.Move.Bar).add(prioAction, enter).
			add(prioNav, keys.ToSidebar.Bar).add(prioQuit, keys.Quit.Bar)
	}
	if m.sidebar.ActivePage() == uikit.PageInfo {
		return helpBar{}.add(prioNav, keys.Scroll.Bar, keys.BackSidebar.Bar).add(prioQuit, keys.Quit.Bar)
	}
	if m.sidebar.ActivePage() == uikit.PageDebug {
		return helpBar{}.add(prioNav, keys.Scroll.Bar, keys.LogJump.Bar, keys.BackSidebar.Bar).add(prioQuit, keys.Quit.Bar)
	}
	bar := helpBar{}.add(prioNav, keys.Move.Bar, keys.Open.Bar)
	if name := m.sidebar.ActiveTask(); name != "" {
		bar = bar.add(prioAction, m.taskHeader(name).Hints()...).
			add(prioNav, keys.Filter.Bar).
			add(prioAction, keys.TaskInfo.Bar).
			add(prioNav, keys.ToSidebar.Bar)
	} else {
		bar = bar.add(prioNav, keys.Filter.Bar, keys.Select.Bar, keys.ToSidebar.Bar)
	}
	if m.sidebar.ActivePage() == uikit.PageHome && m.notifications.PanelHeight() > 0 {
		bar = bar.add(prioNav, keys.NotifPanel.Bar)
	}
	return bar.add(prioQuit, keys.Quit.Bar)
}

// buildSelectionHelpText renders the bottom-bar action set shown while one or
// more runs are multi-selected in the executions list.
func (m Model) buildSelectionHelpText() helpBar {
	count := fmt.Sprintf("%d selected", m.execList.SelectionCount())
	return helpBar{}.add(prioAction, count, keys.BulkDelete.Bar, keys.BulkCancel.Bar, keys.BulkRerun.Bar).
		add(prioNav, keys.SelectAll.Bar, keys.ClearSelect.Bar)
}
