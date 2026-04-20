// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	tea "github.com/charmbracelet/bubbletea"
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
				m.execView.pane.ScrollUp(3)
				m.execView.headerFocus = headerFocusNone
			} else if m.sidebar.ActivePage() == PageInfo && x >= sidebarWidth {
				m.infoView.ScrollUp(3)
			} else if m.sidebar.ActivePage() == PageDebug && x >= sidebarWidth {
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
				m.execView.pane.ScrollDown(3)
				m.execView.headerFocus = headerFocusNone
			} else if m.sidebar.ActivePage() == PageInfo && x >= sidebarWidth {
				m.infoView.ScrollDown(3)
			} else if m.sidebar.ActivePage() == PageDebug && x >= sidebarWidth {
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

	// Sidebar click.
	if x < sidebarWidth {
		prevPage := m.sidebar.ActivePage()
		prevTask := m.sidebar.ActiveTask()
		m.sidebar.handleClick(y)
		focusCmd := m.focusSidebar()
		// Close exec view when clicking sidebar items.
		if m.execView != nil {
			m.closeExecView()
		}
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

	if m.sidebar.ActivePage() == PageHome {
		if m.sidebar.ActiveTask() != "" {
			// Task header: check Run Now button click.
			if y == m.layout.taskBtnY {
				return m, m.confirmAction(confirmActionTrigger)
			}
		} else {
			// Home header: check field row clicks.
			fieldsStartY := m.layout.homeFieldsY
			fields := homeFields(m.info, m.hasLaunchTicket())
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
	// Sidebar hover.
	if x < sidebarWidth {
		m.sidebar.SetHovered(m.sidebar.rowIndexAt(y))
		m.execList.SetHovered(-1)
		m.mouse.homeHover = -1
		if m.execView != nil {
			m.execView.hoveredHeader = headerFocusNone
		}
		return
	}

	m.sidebar.SetHovered(-1)

	// Exec view button hover — use precise X ranges.
	if m.execView != nil {
		m.execView.hoveredHeader = m.execView.hitAt(x, y)
		m.mouse.homeHover = -1
		return
	}

	// Home page hover: fields + exec list.
	if m.sidebar.ActivePage() == PageHome {

		// Home field hover (only when no task is selected).
		if m.sidebar.ActiveTask() == "" {
			fieldsStartY := m.layout.homeFieldsY
			fields := homeFields(m.info, m.hasLaunchTicket())
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
	if m.execView == nil || m.execView.run == nil {
		return m, nil
	}
	// Clear keyboard header focus on any click.
	m.execView.headerFocus = headerFocusNone

	switch m.execView.hitAt(x, y) {
	case headerFocusBack:
		m.closeExecView()
		return m, nil
	case headerFocusID, headerFocusStarted, headerFocusDuration:
		return m, m.dialogs.CopyToClipboard(m.execView.copyValueFor(m.execView.hitAt(x, y)))
	case headerFocusAction:
		switch m.execView.Action() {
		case execViewActionStop:
			return m, m.confirmAction(confirmActionStop)
		case execViewActionRetry:
			return m, m.confirmAction(confirmActionRetry)
		}
	}

	return m, nil
}
