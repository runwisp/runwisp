// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/execlist"
	"github.com/runwisp/runwisp/internal/tui/views/home"
	"github.com/runwisp/runwisp/internal/tui/views/logsearch"
	"github.com/runwisp/runwisp/internal/tui/views/notifications"
)

// keyHandlerFn processes a key event. Returns (model, cmd, handled): when
// handled=true the caller returns early; when handled=false the caller
// delegates to the focused sub-component. An extra cmd with handled=false
// signals "prepend this cmd to the delegation result".
type keyHandlerFn func(m Model, msg tea.KeyMsg) (Model, tea.Cmd, bool)

// globalKeyHandlers maps key strings to their handler. Keys that share the
// same handler (e.g. "left"/"h") share the same function pointer.
var globalKeyHandlers = map[string]keyHandlerFn{
	keyCtrlC:    handleKeyQuit,
	"q":         handleKeyQuit,
	"n":         handleKeyN,
	"esc":       handleKeyEsc,
	"backspace": handleKeyBackspace,
	"f":         handleKeyF,
	"left":      handleKeyLeft,
	"h":         handleKeyLeft,
	"right":     handleKeyRight,
	"l":         handleKeyRight,
	"enter":     handleKeyEnter,
	"r":         handleKeyR,
	"R":         handleKeyR,
	"s":         handleKeyS,
	"d":         handleKeyD,
	"D":         handleKeyD,
	"up":        handleKeyUp,
	"k":         handleKeyUp,
	"down":      handleKeyDown,
	"j":         handleKeyDown,
	"?":         handleKeyHelp,
}

// handleKey processes keyboard input. Global shortcuts are dispatched through
// globalKeyHandlers; unrecognised keys delegate to the focused sub-component.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.logSearch != nil {
		return m.handleLogSearchKey(msg)
	}
	if msg.String() == "/" && m.canOpenLogSearch() {
		return m.openLogSearch()
	}
	if handler, ok := globalKeyHandlers[msg.String()]; ok {
		newM, extraCmd, handled := handler(m, msg)
		if handled {
			return newM, extraCmd
		}
		m = newM
		if extraCmd != nil {
			delegatedM, delegatedCmd := newM.delegateKeyToFocusedView(msg)
			return delegatedM, tea.Batch(extraCmd, delegatedCmd)
		}
	}
	return m.delegateKeyToFocusedView(msg)
}

// delegateKeyToFocusedView forwards msg to whichever sub-component currently
// owns keyboard focus.
func (m Model) delegateKeyToFocusedView(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	if m.execView != nil && m.panelFocus != uikit.PanelSidebar {
		if cmd := m.execView.Update(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
		if cmd := m.maybeLoadOlderLogs(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	} else if m.panelFocus == uikit.PanelSidebar {
		prevPage := m.sidebar.ActivePage()
		prevTask := m.sidebar.ActiveTask()
		m.sidebar.Update(msg)
		if cmd := m.applySidebarSelectionChange(prevPage, prevTask); cmd != nil {
			cmds = append(cmds, cmd)
		}
	} else if m.panelFocus == uikit.PanelMain && m.sidebar.ActivePage() == uikit.PageInfo {
		m.infoView.Update(msg)
	} else if m.panelFocus == uikit.PanelMain && m.sidebar.ActivePage() == uikit.PageDebug {
		m.debugView.Update(msg)
	} else if m.panelFocus == uikit.PanelMain && m.homeCursor < 0 {
		cmds = append(cmds, delegateToExecList(&m, msg)...)
	}
	return m, tea.Batch(cmds...)
}

func delegateToExecList(m *Model, msg tea.KeyMsg) []tea.Cmd {
	var cmds []tea.Cmd
	if cmd := m.execList.Update(msg); cmd != nil {
		cmds = append(cmds, cmd)
	}
	if m.execList.NeedsFetch() {
		cmds = append(cmds, m.fetchExecWindow())
	}
	return cmds
}

// ---------- per-key handlers ----------

func handleKeyQuit(m Model, msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	m.showQuitConfirm()
	return m, nil, true
}

// handleKeyHelp opens the keyboard-shortcut overlay. Confirm/copy dialogs and
// the log-search overlay intercept keys before this handler runs, so no extra
// guards are needed here.
func handleKeyHelp(m Model, msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	m.dialogs.ShowHelp()
	return m, nil, true
}

func handleKeyN(m Model, msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if m.execView == nil && m.sidebar.ActivePage() == uikit.PageHome {
		m.notifications.Toggle()
		m.updateLayout()
		return m, nil, true
	}
	return m, nil, false
}

func handleKeyEsc(m Model, msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if m.notifications.IsExpanded() {
		m.notifications.Toggle()
		m.updateLayout()
		return m, nil, true
	}
	if m.execView != nil {
		if m.execView.Fullscreen() {
			m.execView.ToggleFullscreen()
			m.updateLayout()
			return m, m.syncMouseState(), true
		}
		return m, m.closeExecView(), true
	}
	if m.panelFocus == uikit.PanelMain {
		return m, m.focusSidebar(), true
	}
	return m, nil, false
}

func handleKeyBackspace(m Model, msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if m.execView != nil {
		if m.execView.Fullscreen() {
			m.execView.ToggleFullscreen()
			m.updateLayout()
			return m, m.syncMouseState(), true
		}
		return m, m.closeExecView(), true
	}
	return m, nil, false
}

func handleKeyF(m Model, msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if m.execView != nil {
		m.execView.ToggleFullscreen()
		if m.execView.Fullscreen() {
			m.panelFocus = uikit.PanelMain
			m.execView.SetFocused(true)
		}
		m.updateLayout()
		return m, m.syncMouseState(), true
	}
	return m, nil, false
}

func handleKeyLeft(m Model, msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if m.execView == nil {
		return handleKeyLeftNoExecView(m)
	}
	if m.execView.Fullscreen() {
		return m, nil, false
	}
	if isAtExecViewLeftEdge(m.execView) {
		return m, m.focusSidebar(), true
	}
	return m, nil, false
}

func handleKeyLeftNoExecView(m Model) (Model, tea.Cmd, bool) {
	if m.panelFocus == uikit.PanelMain && m.sidebar.ActivePage() == uikit.PageDebug && m.debugView.pane.HScroll > 0 {
		return m, nil, false
	}
	return m, m.focusSidebar(), true
}

func isAtExecViewLeftEdge(ev *execlist.ExecView) bool {
	return ev.HeaderFocus == execlist.HeaderFocusBack ||
		ev.HeaderFocus == execlist.HeaderFocusStarted ||
		(ev.HeaderFocus == execlist.HeaderFocusNone && ev.Pane.HScroll <= 0)
}

func handleKeyRight(m Model, msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if m.execView == nil {
		if m.panelFocus == uikit.PanelMain && m.sidebar.ActivePage() == uikit.PageDebug {
			return m, nil, false
		}
		return m, m.focusMainPanel(), true
	}
	if m.execView.Fullscreen() {
		return m, nil, false
	}
	if m.panelFocus == uikit.PanelSidebar {
		return m, m.focusMainPanel(), true
	}
	return m, nil, false
}

func handleKeyEnter(m Model, msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if m.notifications.IsExpanded() {
		return handleKeyEnterNotifications(m)
	}
	if m.execView != nil && m.panelFocus == uikit.PanelMain && m.execView.HeaderFocus != execlist.HeaderFocusNone {
		return handleKeyEnterHeader(m)
	}
	if m.panelFocus == uikit.PanelMain && m.execView == nil {
		return handleKeyEnterMainPanel(m)
	}
	// Sidebar: close any open exec view, then delegate so the sidebar can react.
	if m.panelFocus == uikit.PanelSidebar && m.execView != nil {
		return m, m.closeExecView(), false
	}
	return m, nil, false
}

func handleKeyEnterNotifications(m Model) (Model, tea.Cmd, bool) {
	sel := m.notifications.Selected()
	if sel != nil && sel.RunID != "" {
		m.notifications.Toggle()
		m.updateLayout()
		return m, m.openRunByID(sel.TaskName, sel.RunID), true
	}
	return m, nil, true
}

func handleKeyEnterHeader(m Model) (Model, tea.Cmd, bool) {
	switch m.execView.HeaderFocus {
	case execlist.HeaderFocusBack:
		return m, m.closeExecView(), true
	case execlist.HeaderFocusAction:
		return handleKeyEnterActionButton(m)
	case execlist.HeaderFocusStarted, execlist.HeaderFocusDuration, execlist.HeaderFocusID:
		return m, m.copyExecField(), true
	}
	return m, nil, true
}

func handleKeyEnterActionButton(m Model) (Model, tea.Cmd, bool) {
	switch m.execView.Action() {
	case execlist.ActionStop:
		return m, m.confirmAction(confirmActionStop), true
	case execlist.ActionStopService:
		return m, m.confirmAction(confirmActionStopService), true
	case execlist.ActionRetry:
		return m, m.confirmAction(confirmActionRetry), true
	case execlist.ActionRestartService:
		return m, m.confirmAction(confirmActionRestartService), true
	case execlist.ActionDelete:
		return m, m.confirmAction(confirmActionDelete), true
	}
	return m, nil, true
}

func handleKeyEnterMainPanel(m Model) (Model, tea.Cmd, bool) {
	if m.homeCursor >= 0 {
		return m, m.activateHomeField(), true
	}
	if run := m.execList.SelectedRun(); run != nil {
		return m, m.openExecView(run), true
	}
	return m, nil, false
}

func handleKeyR(m Model, msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if m.notifications.IsExpanded() {
		return m, m.toggleSelectedNotificationRead(), true
	}
	if msg.String() == "R" {
		return m, nil, false
	}
	if m.execView != nil {
		return handleKeyRExecView(m)
	}
	return m, m.confirmAction(confirmActionTrigger), true
}

func handleKeyRExecView(m Model) (Model, tea.Cmd, bool) {
	switch m.execView.Action() {
	case execlist.ActionRetry:
		return m, m.confirmAction(confirmActionRetry), true
	case execlist.ActionRestartService:
		return m, m.confirmAction(confirmActionRestartService), true
	}
	return m, nil, true
}

func handleKeyS(m Model, msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if m.execView != nil {
		switch m.execView.Action() {
		case execlist.ActionStop:
			return m, m.confirmAction(confirmActionStop), true
		case execlist.ActionStopService:
			return m, m.confirmAction(confirmActionStopService), true
		}
		return m, nil, true
	}
	return m, nil, false
}

func handleKeyD(m Model, msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if m.execView == nil || m.execView.Fullscreen() {
		return m, nil, false
	}
	if msg.String() == "D" {
		if m.execView.CanDelete() {
			return m, m.confirmAction(confirmActionDelete), true
		}
		return m, nil, true
	}
	return m, m.downloadExecLog(), true
}

func handleKeyUp(m Model, msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if m.notifications.IsExpanded() {
		if m.notifications.MoveCursor(-1) {
			return m, nil, true
		}
		m.notifications.BumpBoundaryFlash()
		return m, notifications.ScheduleFlashClear(), true
	}
	if m.panelFocus == uikit.PanelMain && m.execView == nil && m.sidebar.ActivePage() == uikit.PageHome && m.sidebar.ActiveTask() == "" {
		return handleKeyUpHome(m)
	}
	return m, nil, false
}

func handleKeyUpHome(m Model) (Model, tea.Cmd, bool) {
	if m.execList.Cursor() != 0 && m.execList.TotalCount() != 0 {
		return m, nil, false
	}
	fields := home.Fields(m.info, m.hasLaunchTicket())
	if len(fields) == 0 {
		return m, nil, false
	}
	if m.homeCursor < 0 {
		return m, m.focusHomeField(len(fields) - 1), true
	}
	if m.homeCursor > 0 {
		m.homeCursor--
	}
	return m, m.dialogs.SyncMouseState(), true
}

func handleKeyDown(m Model, msg tea.KeyMsg) (Model, tea.Cmd, bool) {
	if m.notifications.IsExpanded() {
		if m.notifications.MoveCursor(1) {
			return m, nil, true
		}
		m.notifications.BumpBoundaryFlash()
		return m, notifications.ScheduleFlashClear(), true
	}
	if m.panelFocus == uikit.PanelMain && m.execView == nil && m.homeCursor >= 0 {
		return handleKeyDownHome(m)
	}
	return m, nil, false
}

func handleKeyDownHome(m Model) (Model, tea.Cmd, bool) {
	fields := home.Fields(m.info, m.hasLaunchTicket())
	if m.homeCursor < len(fields)-1 {
		m.homeCursor++
	} else {
		m.homeCursor = -1
		m.execList.SetFocused(true)
	}
	return m, m.dialogs.SyncMouseState(), true
}

// canOpenLogSearch reports whether a `/` key press should open the search
// overlay. The feature is scoped to a task — the user must either be
// looking at an exec view or have a task selected in the sidebar.
func (m Model) canOpenLogSearch() bool {
	if m.client == nil {
		return false
	}
	if m.execView != nil && m.execView.Run != nil {
		return true
	}
	return m.sidebar.ActiveTask() != ""
}

// openLogSearch creates and attaches the search overlay scoped to whichever
// task the user is currently focused on.
func (m Model) openLogSearch() (tea.Model, tea.Cmd) {
	taskName := ""
	if m.execView != nil && m.execView.Run != nil {
		taskName = m.execView.Run.TaskName
	} else {
		taskName = m.sidebar.ActiveTask()
	}
	if taskName == "" {
		return m, nil
	}
	ls := logsearch.New(m.client, taskName)
	m.logSearch = &ls
	return m, nil
}

// handleLogSearchKey routes one key event through the overlay. Esc closes;
// Enter on a hit selects (opening the run and scheduling the highlight).
func (m Model) handleLogSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "esc" {
		m.logSearch = nil
		return m, nil
	}
	newLS, cmd := m.logSearch.Update(msg)
	m.logSearch = &newLS
	return m, cmd
}

// handleLogSearchSelect is invoked when the user presses Enter on a hit.
// Closes the overlay, opens the run (if not already), and records the line
// number the pane should land on once its buffer is ready.
func (m Model) handleLogSearchSelect(msg logsearch.SelectMsg) (tea.Model, tea.Cmd) {
	m.logSearch = nil
	m.pendingHighlight = msg.Line
	if m.execView != nil && m.execView.Run != nil && m.execView.Run.ID == msg.RunID {
		// Already open — jump immediately and clear the pending marker.
		m.execView.Pane.JumpToLine(msg.Line)
		m.pendingHighlight = 0
		return m, nil
	}
	return m, m.openRunByID(msg.TaskName, msg.RunID)
}
