// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// handleKey processes keyboard input. Recognised global shortcuts (quit, esc,
// arrows, action keys) are handled inline; anything else is delegated to the
// currently focused sub-component.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg.String() {
	case "ctrl+c", "q":
		m.showQuitConfirm()
		return m, nil

	case "n":
		if m.execView == nil && m.sidebar.ActivePage() == PageHome {
			m.notifications.Toggle()
			m.updateLayout()
			return m, nil
		}

	case "esc":
		if m.notifications.IsExpanded() {
			m.notifications.Toggle()
			m.updateLayout()
			return m, nil
		}
		if m.execView != nil {
			if m.execView.Fullscreen() {
				m.execView.ToggleFullscreen()
				m.updateLayout()
				return m, m.syncMouseState()
			}
			return m, m.closeExecView()
		}
		if m.panelFocus == PanelMain {
			return m, m.focusSidebar()
		}

	case "backspace":
		if m.execView != nil {
			if m.execView.Fullscreen() {
				m.execView.ToggleFullscreen()
				m.updateLayout()
				return m, m.syncMouseState()
			}
			return m, m.closeExecView()
		}

	case "f":
		if m.execView != nil {
			m.execView.ToggleFullscreen()
			if m.execView.Fullscreen() {
				m.panelFocus = PanelMain
				m.execView.SetFocused(true)
			}
			m.updateLayout()
			return m, m.syncMouseState()
		}

	case "left", "h":
		if m.execView == nil {
			if m.panelFocus == PanelMain && m.sidebar.ActivePage() == PageDebug && m.debugView.pane.hScroll > 0 {
				break
			}
			return m, m.focusSidebar()
		}

		ev := m.execView
		if ev.Fullscreen() {
			// In fullscreen the sidebar is hidden; left just scrolls the pane.
			break
		}
		atEdge := ev.headerFocus == headerFocusBack || ev.headerFocus == headerFocusStarted ||
			(ev.headerFocus == headerFocusNone && ev.pane.hScroll <= 0)
		if atEdge {
			return m, m.focusSidebar()
		}

	case "right", "l":
		if m.execView == nil {
			if m.panelFocus == PanelMain && m.sidebar.ActivePage() == PageDebug {
				break
			}
			return m, m.focusMainPanel()
		}
		if m.execView.Fullscreen() {
			break
		}
		if m.panelFocus == PanelSidebar {
			return m, m.focusMainPanel()
		}

	case "enter":
		if m.notifications.IsExpanded() {
			if sel := m.notifications.Selected(); sel != nil && sel.RunID != "" {
				m.notifications.Toggle()
				m.updateLayout()
				return m, m.openRunByID(sel.TaskName, sel.RunID)
			}
			return m, nil
		}
		if m.execView != nil && m.panelFocus == PanelMain && m.execView.headerFocus != headerFocusNone {
			switch m.execView.headerFocus {
			case headerFocusBack:
				return m, m.closeExecView()
			case headerFocusAction:
				switch m.execView.Action() {
				case execViewActionStop:
					return m, m.confirmAction(confirmActionStop)
				case execViewActionRetry:
					return m, m.confirmAction(confirmActionRetry)
				}
			case headerFocusStarted, headerFocusDuration, headerFocusID:
				return m, m.copyExecField()
			}
			return m, nil
		}
		if m.panelFocus == PanelMain && m.execView == nil {
			if m.homeCursor >= 0 {
				return m, m.activateHomeField()
			}
			if run := m.execList.SelectedRun(); run != nil {
				return m, m.openExecView(run)
			}
		}
		// Enter on sidebar commits the cursor item as the main view;
		// close any open exec view so the user lands on that view.
		if m.panelFocus == PanelSidebar && m.execView != nil {
			if cmd := m.closeExecView(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}

	case "r":
		if m.notifications.IsExpanded() {
			return m, m.streams.MarkNotificationsRead(time.Now())
		}
		if m.execView != nil {
			return m, m.confirmAction(confirmActionRetry)
		}
		return m, m.confirmAction(confirmActionTrigger)

	case "s":
		if m.execView != nil {
			return m, m.confirmAction(confirmActionStop)
		}

	case "up", "k":
		if m.notifications.IsExpanded() {
			m.notifications.MoveCursor(-1)
			return m, nil
		}
		if m.panelFocus == PanelMain && m.execView == nil && m.sidebar.ActivePage() == PageHome && m.sidebar.ActiveTask() == "" {
			if m.execList.Cursor() == 0 || m.execList.totalCount() == 0 {
				fields := homeFields(m.info, m.hasLaunchTicket())
				if len(fields) > 0 {
					if m.homeCursor < 0 {
						return m, m.focusHomeField(len(fields) - 1)
					} else if m.homeCursor > 0 {
						m.homeCursor--
					}
					return m, m.dialogs.SyncMouseState()
				}
			}
		}

	case "down", "j":
		if m.notifications.IsExpanded() {
			m.notifications.MoveCursor(1)
			return m, nil
		}
		if m.panelFocus == PanelMain && m.execView == nil && m.homeCursor >= 0 {
			fields := homeFields(m.info, m.hasLaunchTicket())
			if m.homeCursor < len(fields)-1 {
				m.homeCursor++
			} else {
				m.homeCursor = -1
				m.execList.SetFocused(true)
			}
			return m, m.dialogs.SyncMouseState()
		}
	}

	// Delegate to focused sub-component.
	if m.execView != nil && m.panelFocus != PanelSidebar {
		if cmd := m.execView.Update(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	} else if m.panelFocus == PanelSidebar {
		prevPage := m.sidebar.ActivePage()
		prevTask := m.sidebar.ActiveTask()
		m.sidebar.Update(msg)
		if cmd := m.applySidebarSelectionChange(prevPage, prevTask); cmd != nil {
			cmds = append(cmds, cmd)
		}
	} else if m.panelFocus == PanelMain && m.sidebar.ActivePage() == PageInfo {
		m.infoView.Update(msg)
	} else if m.panelFocus == PanelMain && m.sidebar.ActivePage() == PageDebug {
		m.debugView.Update(msg)
	} else if m.panelFocus == PanelMain && m.homeCursor < 0 {
		if cmd := m.execList.Update(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if m.execList.NeedsFetch() {
			cmds = append(cmds, m.fetchExecWindow())
		}
	}

	return m, tea.Batch(cmds...)
}
