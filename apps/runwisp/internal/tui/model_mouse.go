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

	// Always update hover coordinates on any mouse event.
	m.mouse.hoverX = x
	m.mouse.hoverY = y

	// Compute hover state for every mouse event (motion or click).
	m.updateHoverState(x, y)

	if msg.Action == tea.MouseActionMotion {
		return m, nil
	}

	// Mouse wheel scrolling.
	if msg.Action == tea.MouseActionPress {
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			if m.execView != nil {
				m.execView.Pane.ScrollUp(3)
				m.execView.HeaderFocus = execlist.HeaderFocusNone
			} else if m.sidebar.ActivePage() == uikit.PageInfo && x >= uikit.SidebarWidth {
				m.infoView.ScrollUp(3)
			} else if m.sidebar.ActivePage() == uikit.PageDebug && x >= uikit.SidebarWidth {
				m.debugView.ScrollUp(3)
			} else {
				m.execList.ScrollBy(-3)
				if m.execList.NeedsFetch() {
					return m, m.fetchExecWindow()
				}
			}
			return m, nil
		case tea.MouseButtonWheelDown:
			if m.execView != nil {
				m.execView.Pane.ScrollDown(3)
				m.execView.HeaderFocus = execlist.HeaderFocusNone
			} else if m.sidebar.ActivePage() == uikit.PageInfo && x >= uikit.SidebarWidth {
				m.infoView.ScrollDown(3)
			} else if m.sidebar.ActivePage() == uikit.PageDebug && x >= uikit.SidebarWidth {
				m.debugView.ScrollDown(3)
			} else {
				m.execList.ScrollBy(3)
				if m.execList.NeedsFetch() {
					return m, m.fetchExecWindow()
				}
			}
			return m, nil
		}
	}

	// Only process left-button presses below.
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}

	// home.Sidebar click.
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

	// Main panel click.

	// Click on exec view action buttons.
	if m.execView != nil {
		return m.handleExecViewClick(x, y)
	}

	if m.sidebar.ActivePage() == uikit.PageHome {
		if m.sidebar.ActiveTask() != "" {
			// Task header: check Run Now button click.
			if y == m.layout.taskBtnY {
				return m, m.confirmAction(confirmActionTrigger)
			}
		} else {
			// Home header: check field row clicks.
			fieldsStartY := m.layout.homeFieldsY
			fields := home.Fields(m.info, m.hasLaunchTicket())
			fieldIdx := y - fieldsStartY
			if fieldIdx >= 0 && fieldIdx < len(fields) {
				focusCmd := m.focusHomeField(fieldIdx)
				isDoubleClick := m.detectDoubleClick(y)
				if isDoubleClick {
					return m, m.activateHomeField()
				}
				return m, focusCmd
			}
		}

		// Click on exec list area.
		focusCmd := m.focusMainPanel()
		localY := y - m.mainHeaderHeight()
		if localY >= 0 {
			isDoubleClick := m.detectDoubleClick(y)
			hit := m.execList.HandleClick(localY)
			if hit && isDoubleClick {
				if run := m.execList.SelectedRun(); run != nil {
					cmd := m.openExecView(run)
					return m, tea.Batch(focusCmd, cmd)
				}
			}
		}

		return m, focusCmd
	}

	return m, nil
}

// updateHoverState computes hover highlights for all UI zones based on mouse position.
func (m *Model) updateHoverState(x, y int) {
	// home.Sidebar hover.
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
