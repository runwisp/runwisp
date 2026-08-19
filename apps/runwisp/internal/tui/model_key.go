// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	tea "charm.land/bubbletea/v2"
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
type keyHandlerFn func(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd, bool)

// globalKeyHandlers maps key strings to their handler. Keys that share the
// same handler (e.g. "left"/"h") share the same function pointer.
var globalKeyHandlers = map[string]keyHandlerFn{
	keyCtrlC:    handleKeyQuit,
	"q":         handleKeyQuit,
	"n":         handleKeyN,
	"a":         handleKeyA,
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
	"i":         handleKeyI,
	"u":         handleKeyU,
	"s":         handleKeyS,
	"d":         handleKeyD,
	"D":         handleKeyD,
	"up":        handleKeyUp,
	"k":         handleKeyUp,
	"down":      handleKeyDown,
	"j":         handleKeyDown,
	"[":         handleKeyPrevAnchor,
	"]":         handleKeyNextAnchor,
	"?":         handleKeyHelp,
}

// handleKey processes keyboard input. Global shortcuts are dispatched through
// globalKeyHandlers; unrecognised keys delegate to the focused sub-component.
func (m Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.logSearch != nil {
		return m.handleLogSearchKey(msg)
	}
	if m.sidebar.Filtering() {
		return m.handleSidebarFilterKey(msg)
	}
	if msg.String() == "/" {
		// `/` filters tasks when the sidebar is focused, and searches logs of the
		// focused task otherwise.
		if m.panelFocus == uikit.PanelSidebar {
			m.sidebar.StartFilter()
			return m, nil
		}
		if m.canOpenLogSearch() {
			return m.openLogSearch()
		}
	}
	if m.runListFocused() {
		if newM, cmd, handled := m.handleRunListSelectionKey(msg); handled {
			return newM, cmd
		}
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
func (m Model) delegateKeyToFocusedView(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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

func delegateToExecList(m *Model, msg tea.KeyPressMsg) []tea.Cmd {
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

func handleKeyQuit(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	m.showQuitConfirm()
	return m, nil, true
}

// handleKeyHelp opens the keyboard-shortcut overlay. Confirm/copy dialogs and
// the log-search overlay intercept keys before this handler runs, so no extra
// guards are needed here.
func handleKeyHelp(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	m.dialogs.ShowHelp()
	return m, nil, true
}

func handleKeyN(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	if m.execView == nil && m.sidebar.ActivePage() == uikit.PageHome {
		m.notifications.Toggle()
		m.updateLayout()
		return m, nil, true
	}
	return m, nil, false
}

// handleKeyA marks every notification read while the notifications panel is
// expanded. Elsewhere it falls through so `a` stays free for sub-components.
func handleKeyA(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	if m.notifications.IsExpanded() {
		return m, m.markAllNotificationsRead(), true
	}
	return m, nil, false
}

func handleKeyEsc(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
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

func handleKeyBackspace(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
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

func handleKeyF(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
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

func handleKeyLeft(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
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

func handleKeyRight(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
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

func handleKeyEnter(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	if m.notifications.IsExpanded() {
		return handleKeyEnterNotifications(m)
	}
	if m.execView != nil && m.panelFocus == uikit.PanelMain && m.execView.HeaderFocus != execlist.HeaderFocusNone {
		return handleKeyEnterHeader(m)
	}
	if m.panelFocus == uikit.PanelMain && m.execView == nil {
		return handleKeyEnterMainPanel(m)
	}
	// Pane focused on a frame-history anchor: open the viewer.
	if newM, cmd, handled := m.openCursorFrameHistory(); handled {
		return newM, cmd, true
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
	case execlist.HeaderFocusParams:
		return m, m.showRunParams(), true
	}
	return m, nil, true
}

func handleKeyEnterActionButton(m Model) (Model, tea.Cmd, bool) {
	if ca, ok := actionConfirm(m.execView.Action()); ok {
		return m, m.confirmAction(ca), true
	}
	return m, nil, true
}

// paneAnchorNavActive reports whether the log pane is focused for frame-history
// navigation: an exec view is open (not fullscreen, so line-number gutter and
// markers are visible) with the main panel focused and no header item selected.
func (m Model) paneAnchorNavActive() bool {
	return m.execView != nil &&
		!m.execView.Fullscreen() &&
		m.panelFocus == uikit.PanelMain &&
		m.execView.HeaderFocus == execlist.HeaderFocusNone
}

// handleKeyPrevAnchor / handleKeyNextAnchor move the pane's anchor cursor to the
// previous/next line that carries frame history. They yield to delegation when
// the pane isn't focused or has no anchors, so `[`/`]` stay free elsewhere.
func handleKeyPrevAnchor(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	return handleAnchorNav(m, -1)
}

func handleKeyNextAnchor(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	return handleAnchorNav(m, 1)
}

func handleAnchorNav(m Model, dir int) (Model, tea.Cmd, bool) {
	if !m.paneAnchorNavActive() || !m.execView.Pane.HasAnchors() {
		return m, nil, false
	}
	m.execView.Pane.MoveCursorToAnchor(dir)
	return m, nil, true
}

// openCursorFrameHistory fetches the frames for the anchor under the pane cursor
// and (on success) opens the viewer. Returns false when the cursor isn't on an
// anchor so Enter falls through to its other meanings.
func (m Model) openCursorFrameHistory() (Model, tea.Cmd, bool) {
	if !m.paneAnchorNavActive() {
		return m, nil, false
	}
	lineNum, _, ok := m.execView.Pane.CursorAnchor()
	if !ok {
		return m, nil, false
	}
	committed := ""
	if idx := m.execView.Pane.Cursor; idx >= 0 && idx < len(m.execView.Pane.Lines) {
		committed = m.execView.Pane.Lines[idx].Text
	}
	cmd := m.streams.FetchLineHistory(m.execView.Run.ID, lineNum, committed)
	return m, cmd, true
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

func handleKeyR(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	if m.notifications.IsExpanded() {
		return m, m.toggleSelectedNotificationRead(), true
	}
	if msg.String() == "R" {
		return m, m.reloadConfig(), true
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

// handleKeyU fires the inverse of the most recent undoable action while its
// toast is still showing. With no pending undo it falls through so `u` stays
// free for sub-components.
func handleKeyU(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	if cmd := m.dialogs.TakeUndo(); cmd != nil {
		return m, cmd, true
	}
	return m, nil, false
}

// handleKeyI opens an on-demand inspector for whatever is in focus: the open
// run when an exec view is showing, otherwise the focused task (with an async
// health fetch). With nothing inspectable it falls through.
func handleKeyI(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	if m.execView != nil && m.execView.Run != nil {
		m.dialogs.ShowRunDetail(m.execView.Run, m.execView.TaskIsService, m.execView.InstanceCount)
		return m, nil, true
	}
	taskName := m.inspectTaskName()
	if taskName == "" {
		return m, nil, false
	}
	m.dialogs.ShowTaskDetail(taskName, m.taskDisplayByName(taskName))
	return m, m.streams.FetchTaskSummary(taskName), true
}

// inspectTaskName picks the task the inspector should describe. While the
// sidebar is focused it follows the cursor (the task you're looking at, even
// before selecting it); otherwise it falls back to the focused task — an open
// exec view's task, else the sidebar selection.
func (m Model) inspectTaskName() string {
	if m.execView == nil && m.panelFocus == uikit.PanelSidebar {
		return m.sidebar.CursorTaskName()
	}
	return m.focusedTaskName()
}

func handleKeyS(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
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

func handleKeyD(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
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

func handleKeyUp(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
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

func handleKeyDown(m Model, msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
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

// focusedTaskName returns the task the user is currently working with: an open
// exec view's task takes precedence over the sidebar selection. Empty when no
// task is in focus (e.g. the Home/Info/Debug pages).
func (m Model) focusedTaskName() string {
	if m.execView != nil && m.execView.Run != nil {
		return m.execView.Run.TaskName
	}
	return m.sidebar.ActiveTask()
}

// openLogSearch creates and attaches the search overlay scoped to whichever
// task the user is currently focused on.
func (m Model) openLogSearch() (tea.Model, tea.Cmd) {
	taskName := m.focusedTaskName()
	if taskName == "" {
		return m, nil
	}
	ls := logsearch.New(m.client, taskName)
	m.logSearch = &ls
	return m, nil
}

// runListFocused reports whether the executions list is the active, interactive
// surface: the Home page main panel is focused with no exec view open, the
// notifications panel collapsed, and the cursor off the home-overview fields.
// Only then do the run-list multi-select keys (space/a/c/e/d/esc) apply.
func (m Model) runListFocused() bool {
	return m.execView == nil &&
		m.panelFocus == uikit.PanelMain &&
		m.sidebar.ActivePage() == uikit.PageHome &&
		!m.notifications.IsExpanded() &&
		m.homeCursor < 0
}

// handleRunListSelectionKey routes the run-list multi-select keys while the
// executions list is focused: space toggles the cursor row, `a` selects every
// run matching the active filter, and — once a selection exists — `d` deletes
// (undoably), `c` cancels, `e` reruns, and esc clears it. Keys it doesn't own
// return handled=false so normal handling continues.
func (m Model) handleRunListSelectionKey(msg tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	if msg.Code == tea.KeySpace {
		m.execList.ToggleSelectCursor()
		return m, nil, true
	}
	if msg.String() == "a" {
		if m.execList.TotalCount() == 0 {
			return m, nil, false
		}
		m.execList.SelectAllMatching()
		return m, nil, true
	}
	if !m.execList.SelectionActive() {
		return m, nil, false
	}
	switch msg.String() {
	case "esc":
		m.execList.ClearSelection()
		return m, nil, true
	case "d", "D":
		return m, m.bulkDeleteSelection(), true
	case "c":
		return m, m.bulkCancelSelection(), true
	case "e":
		return m, m.bulkRerunSelection(), true
	}
	return m, nil, false
}

// bulkDeleteSelection soft-deletes the selected runs and clears the selection.
// The result returns as a BulkDeleteResultMsg so the handler can arm an undo.
func (m *Model) bulkDeleteSelection() tea.Cmd {
	sel, ok := m.execList.SelectionSelector()
	if !ok {
		return nil
	}
	m.execList.ClearSelection()
	return m.streams.DeleteRunsUndoable(sel)
}

// bulkCancelSelection cancels the selected runs and clears the selection.
func (m *Model) bulkCancelSelection() tea.Cmd {
	sel, ok := m.execList.SelectionSelector()
	if !ok {
		return nil
	}
	m.execList.ClearSelection()
	return m.streams.CancelRuns(sel)
}

// bulkRerunSelection triggers a fresh run for each selected run and clears the
// selection.
func (m *Model) bulkRerunSelection() tea.Cmd {
	sel, ok := m.execList.SelectionSelector()
	if !ok {
		return nil
	}
	m.execList.ClearSelection()
	return m.streams.RerunRuns(sel)
}

// handleSidebarFilterKey routes one key event through the sidebar's
// type-to-filter sub-mode. Every printable character types into the query — so
// `q` filters rather than quitting — while a small set of control keys navigate,
// commit, or cancel. ctrl+c still quits.
func (m Model) handleSidebarFilterKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case keyCtrlC:
		m.showQuitConfirm()
		return m, nil
	case "esc":
		m.sidebar.StopFilter()
		return m, nil
	case "enter":
		prevPage := m.sidebar.ActivePage()
		prevTask := m.sidebar.ActiveTask()
		m.sidebar.SelectFilterCursor()
		return m, m.applySidebarSelectionChange(prevPage, prevTask)
	case "backspace":
		m.sidebar.FilterBackspace()
		return m, nil
	case "up":
		m.sidebar.MoveCursor(-1)
		return m, nil
	case "down":
		m.sidebar.MoveCursor(1)
		return m, nil
	case "pgup":
		m.sidebar.PageCursor(-1)
		return m, nil
	case "pgdown":
		m.sidebar.PageCursor(1)
		return m, nil
	}
	if msg.Text != "" {
		m.sidebar.FilterAppend(msg.Text)
	}
	return m, nil
}

// handleLogSearchKey routes one key event through the overlay. Esc closes;
// Enter on a hit selects (opening the run and scheduling the highlight).
func (m Model) handleLogSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
