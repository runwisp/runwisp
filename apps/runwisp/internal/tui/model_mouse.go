// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/execlist"
	"github.com/runwisp/runwisp/internal/tui/views/home"
)

// handleMouse processes mouse clicks and motion on sidebar, Run Now button, and exec list.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	x, y := msg.X, msg.Y
	m.mouse.hoverX = x
	m.mouse.hoverY = y
	m.updateHoverState(x, y)

	if msg.Action == tea.MouseActionMotion {
		return m, nil
	}

	if msg.Action == tea.MouseActionPress {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			return m, m.scrollWheelUp(x)
		case tea.MouseButtonWheelDown:
			return m, m.scrollWheelDown(x)
		}
	}

	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}

	if x < uikit.SidebarWidth {
		prevPage := m.sidebar.ActivePage()
		prevTask := m.sidebar.ActiveTask()
		m.sidebar.HandleClick(y)
		focusCmd := m.focusSidebar()
		if cmd := m.applySidebarSelectionChange(prevPage, prevTask); cmd != nil {
			return m, tea.Batch(focusCmd, cmd)
		}
		return m, focusCmd
	}

	return m.handleMainPanelClick(x, y)
}

func (m *Model) scrollWheelUp(x int) tea.Cmd {
	if m.execView != nil {
		m.execView.Pane.ScrollUp(3)
		m.execView.HeaderFocus = execlist.HeaderFocusNone
		return nil
	}
	if m.sidebar.ActivePage() == uikit.PageInfo && x >= uikit.SidebarWidth {
		m.infoView.ScrollUp(3)
		return nil
	}
	if m.sidebar.ActivePage() == uikit.PageDebug && x >= uikit.SidebarWidth {
		m.debugView.ScrollUp(3)
		return nil
	}
	m.execList.ScrollBy(-3)
	if m.execList.NeedsFetch() {
		return m.fetchExecWindow()
	}
	return nil
}

func (m *Model) scrollWheelDown(x int) tea.Cmd {
	if m.execView != nil {
		m.execView.Pane.ScrollDown(3)
		m.execView.HeaderFocus = execlist.HeaderFocusNone
		return nil
	}
	if m.sidebar.ActivePage() == uikit.PageInfo && x >= uikit.SidebarWidth {
		m.infoView.ScrollDown(3)
		return nil
	}
	if m.sidebar.ActivePage() == uikit.PageDebug && x >= uikit.SidebarWidth {
		m.debugView.ScrollDown(3)
		return nil
	}
	m.execList.ScrollBy(3)
	if m.execList.NeedsFetch() {
		return m.fetchExecWindow()
	}
	return nil
}

func (m Model) handleMainPanelClick(x, y int) (tea.Model, tea.Cmd) {
	if m.execView != nil {
		return m.handleExecViewClick(x, y)
	}
	if m.sidebar.ActivePage() == uikit.PageHome {
		return m.handleHomePageClick(x, y)
	}
	return m, nil
}

func (m Model) handleHomePageClick(x, y int) (tea.Model, tea.Cmd) {
	if m.sidebar.ActiveTask() != "" {
		if y == m.layout.taskBtnY {
			return m, m.confirmAction(confirmActionTrigger)
		}
	} else {
		fieldsStartY := m.layout.homeFieldsY
		fields := home.Fields(m.info, m.hasLaunchTicket())
		fieldIdx := y - fieldsStartY
		if fieldIdx >= 0 && fieldIdx < len(fields) {
			focusCmd := m.focusHomeField(fieldIdx)
			if m.detectDoubleClick(y) {
				return m, m.activateHomeField()
			}
			return m, focusCmd
		}
	}
	return m.handleExecListClick(y, m.focusMainPanel())
}

func (m Model) handleExecListClick(y int, focusCmd tea.Cmd) (tea.Model, tea.Cmd) {
	localY := y - m.mainHeaderHeight()
	if localY < 0 {
		return m, focusCmd
	}
	if m.execList.HandleClick(localY) && m.detectDoubleClick(y) {
		if run := m.execList.SelectedRun(); run != nil {
			return m, tea.Batch(focusCmd, m.openExecView(run))
		}
	}
	return m, focusCmd
}

// updateHoverState computes hover highlights for all UI zones based on mouse position.
func (m *Model) updateHoverState(x, y int) {
	if x < uikit.SidebarWidth {
		m.sidebar.SetHovered(m.sidebar.RowIndexAt(y))
		m.execList.SetHovered(-1)
		m.mouse.homeHover = -1
		if m.execView != nil {
			m.execView.HoveredHeader = execlist.HeaderFocusNone
		}
		return
	}

	m.sidebar.SetHovered(-1)

	// Exec view button hover — use precise X ranges.
	if m.execView != nil {
		m.execView.HoveredHeader = m.execView.HitAt(x, y)
		m.mouse.homeHover = -1
		return
	}

	// Home page hover: fields + exec list.
	if m.sidebar.ActivePage() == uikit.PageHome {

		// Home field hover (only when no task is selected).
		if m.sidebar.ActiveTask() == "" {
			fieldsStartY := m.layout.homeFieldsY
			fields := home.Fields(m.info, m.hasLaunchTicket())
			fieldIdx := y - fieldsStartY
			if fieldIdx >= 0 && fieldIdx < len(fields) {
				m.mouse.homeHover = fieldIdx
			} else {
				m.mouse.homeHover = -1
			}
		} else {
			m.mouse.homeHover = -1
		}

		localY := y - m.mainHeaderHeight()
		m.execList.SetHoveredFromLocalY(localY)
	} else {
		m.execList.SetHovered(-1)
		m.mouse.homeHover = -1
	}
}

// handleExecViewClick handles clicks on buttons and meta fields inside the exec view.
func (m Model) handleExecViewClick(x, y int) (tea.Model, tea.Cmd) {
	if m.execView == nil || m.execView.Run == nil {
		return m, nil
	}
	// Clear keyboard header focus on any click.
	m.execView.HeaderFocus = execlist.HeaderFocusNone

	switch m.execView.HitAt(x, y) {
	case execlist.HeaderFocusBack:
		return m, m.closeExecView()
	case execlist.HeaderFocusID, execlist.HeaderFocusStarted, execlist.HeaderFocusDuration:
		return m, m.dialogs.CopyToClipboard(m.execView.CopyValueFor(m.execView.HitAt(x, y)))
	case execlist.HeaderFocusAction:
		switch m.execView.Action() {
		case execlist.ActionStop:
			return m, m.confirmAction(confirmActionStop)
		case execlist.ActionStopService:
			return m, m.confirmAction(confirmActionStopService)
		case execlist.ActionRetry:
			return m, m.confirmAction(confirmActionRetry)
		case execlist.ActionRestartService:
			return m, m.confirmAction(confirmActionRestartService)
		}
	}

	return m, nil
}
