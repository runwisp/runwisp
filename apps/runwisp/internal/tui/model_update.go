// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/textutil"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/execlist"
	"github.com/runwisp/runwisp/internal/tui/views/logpane"
	"github.com/runwisp/runwisp/internal/tui/views/logsearch"
)

const keyCtrlC = "ctrl+c"

// Update processes messages by delegating to per-domain dispatchers. Each
// dispatcher owns a related group of message types (input, streams, logs,
// notifications, actions, lifecycle); routing is purely structural so adding
// a new message means picking the right group rather than extending one
// monolithic switch.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if newModel, cmd, intercepted := m.interceptActiveDialog(msg); intercepted {
		return newModel, cmd
	}

	dispatchers := []func(tea.Msg) (tea.Model, tea.Cmd, bool){
		m.dispatchInputMsg,
		m.dispatchStreamMsg,
		m.dispatchLogMsg,
		m.dispatchNotificationMsg,
		m.dispatchActionMsg,
		m.dispatchLifecycleMsg,
	}
	for _, dispatch := range dispatchers {
		if newModel, cmd, ok := dispatch(msg); ok {
			return newModel, cmd
		}
	}
	return m, nil
}

// interceptActiveDialog gives the active modal first claim on the message.
// Dialogs are checked in precedence order; only the active one runs, and it
// reports whether it consumed the message (false lets the dispatchers see it,
// e.g. WindowSizeMsg keeps layout responsive while a dialog is open).
func (m Model) interceptActiveDialog(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	interceptors := []struct {
		active bool
		fn     func(tea.Msg) (tea.Model, tea.Cmd, bool)
	}{
		{m.dialogs.HasConfirm(), m.interceptConfirmDialog},
		{m.dialogs.HasParamForm(), m.interceptParamFormDialog},
		{m.dialogs.HasRunParams(), m.interceptRunParamsDialog},
		{m.dialogs.HasCopy(), m.interceptCopyDialog},
		{m.dialogs.HasLogHistory(), m.interceptLogHistoryDialog},
		{m.dialogs.HasTaskDetail(), m.interceptTaskDetailDialog},
		{m.dialogs.HasRunDetail(), m.interceptRunDetailDialog},
		{m.dialogs.HasHelp(), m.interceptHelpDialog},
	}
	for _, ic := range interceptors {
		if !ic.active {
			continue
		}
		if newModel, cmd, intercepted := ic.fn(msg); intercepted {
			return newModel, cmd, true
		}
	}
	return m, nil, false
}

func (m Model) dispatchInputMsg(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		model, cmd := m.handleWindowSize(msg)
		return model, cmd, true
	case tea.MouseMsg:
		model, cmd := m.handleMouse(msg)
		return model, cmd, true
	case tea.KeyPressMsg:
		model, cmd := m.handleKey(msg)
		return model, cmd, true
	}
	return m, nil, false
}

func (m Model) dispatchStreamMsg(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case uikit.ExecWindowFetchedMsg:
		model, cmd := m.handleExecWindowFetched(msg)
		return model, cmd, true
	case uikit.SSEConnectedMsg:
		model, cmd := m.handleSSEConnected(msg)
		return model, cmd, true
	case uikit.SSEEventMsg:
		model, cmd := m.handleSSEEventMsg(msg)
		return model, cmd, true
	case uikit.SSEDisconnectedMsg:
		model, cmd := m.handleSSEDisconnected()
		return model, cmd, true
	}
	return m, nil, false
}

func (m Model) dispatchLogMsg(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case uikit.LogOlderLoadedMsg:
		model, cmd := m.handleLogOlderLoaded(msg)
		return model, cmd, true
	case uikit.LogTailLoadedMsg:
		model, cmd := m.handleLogTailLoaded(msg)
		return model, cmd, true
	case uikit.LogStreamConnectedMsg:
		model, cmd := m.handleLogStreamConnected(msg)
		return model, cmd, true
	case uikit.LogLineMsg:
		model, cmd := m.handleLogLine(msg)
		return model, cmd, true
	case uikit.LogRegionMsg:
		model, cmd := m.handleLogRegion(msg)
		return model, cmd, true
	case uikit.LogRotatedMsg:
		model, cmd := m.handleLogRotated(msg)
		return model, cmd, true
	case uikit.LogDroppedMsg:
		model, cmd := m.handleLogDropped(msg)
		return model, cmd, true
	case uikit.LogDoneMsg:
		model, cmd := m.handleLogDone(msg)
		return model, cmd, true
	case uikit.DebugLogMsg:
		model, cmd := m.handleDebugLog(msg)
		return model, cmd, true
	case uikit.ReconnectLogMsg:
		model, cmd := m.handleReconnectLog(msg)
		return model, cmd, true
	case uikit.LogLineHistoryMsg:
		model, cmd := m.handleLogLineHistory(msg)
		return model, cmd, true
	case uikit.DaemonLogConnectedMsg:
		model, cmd := m.handleDaemonLogConnected(msg)
		return model, cmd, true
	case uikit.DaemonLogLineMsg:
		model, cmd := m.handleDaemonLogLine(msg)
		return model, cmd, true
	case uikit.DaemonLogDisconnectedMsg:
		model, cmd := m.handleDaemonLogDisconnected()
		return model, cmd, true
	}
	return m, nil, false
}

func (m Model) dispatchNotificationMsg(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case uikit.NotificationStreamConnectedMsg:
		model, cmd := m.handleNotificationStreamConnected(msg)
		return model, cmd, true
	case uikit.NotificationEventMsg:
		model, cmd := m.handleNotificationEvent(msg)
		return model, cmd, true
	case uikit.NotificationStreamDisconnectedMsg:
		model, cmd := m.handleNotificationStreamDisconnected()
		return model, cmd, true
	case uikit.NotificationUnreadCountMsg:
		model, cmd := m.handleNotificationUnreadCount(msg)
		return model, cmd, true
	case uikit.NotificationsLoadedMsg:
		model, cmd := m.handleNotificationsLoaded(msg)
		return model, cmd, true
	case uikit.NotificationReadStateMsg:
		model, cmd := m.handleNotificationReadState(msg)
		return model, cmd, true
	case uikit.NotificationBoundaryFlashClearedMsg:
		m.notifications.ClearBoundaryFlash()
		return m, nil, true
	}
	return m, nil, false
}

func (m Model) dispatchActionMsg(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case uikit.TriggerRunMsg:
		model, cmd := m.handleTriggerRun(msg)
		return model, cmd, true
	case uikit.StopRunMsg:
		model, cmd := m.handleStopRun(msg)
		return model, cmd, true
	case uikit.RestartServiceMsg:
		model, cmd := m.handleRestartService(msg)
		return model, cmd, true
	case uikit.StopServiceMsg:
		model, cmd := m.handleStopService(msg)
		return model, cmd, true
	case uikit.DeleteRunMsg:
		model, cmd := m.handleDeleteRun(msg)
		return model, cmd, true
	case uikit.BulkActionMsg:
		model, cmd := m.handleBulkAction(msg)
		return model, cmd, true
	case uikit.BulkDeleteResultMsg:
		model, cmd := m.handleBulkDeleteResult(msg)
		return model, cmd, true
	}
	return m, nil, false
}

func (m Model) dispatchLifecycleMsg(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case logsearch.SelectMsg:
		model, cmd := m.handleLogSearchSelect(msg)
		return model, cmd, true
	case uikit.TickMsg:
		model, cmd := m.handleTick()
		return model, cmd, true
	case uikit.QuitMsg:
		model, cmd := m.handleQuit(msg)
		return model, cmd, true
	case uikit.FlashExpiredMsg:
		model, cmd := m.handleFlashExpired()
		return model, cmd, true
	case uikit.OpenBrowserMsg:
		model, cmd := m.handleOpenBrowser(msg)
		return model, cmd, true
	case uikit.OpenRunMsg:
		model, cmd := m.handleOpenRun(msg)
		return model, cmd, true
	case uikit.SystemStatsMsg:
		model, cmd := m.handleSystemStats(msg)
		return model, cmd, true
	case uikit.DaemonInfoMsg:
		model, cmd := m.handleDaemonInfo(msg)
		return model, cmd, true
	case uikit.ReloadResultMsg:
		model, cmd := m.handleReloadResult(msg)
		return model, cmd, true
	case uikit.MetricsHistoryMsg:
		model, cmd := m.handleMetricsHistory(msg)
		return model, cmd, true
	case uikit.RunSummaryMsg:
		model, cmd := m.handleRunSummary(msg)
		return model, cmd, true
	case uikit.TaskSummaryMsg:
		m.dialogs.ApplyTaskSummary(msg)
		return m, nil, true
	}
	return m, nil, false
}

func (m Model) handleNotificationStreamConnected(msg uikit.NotificationStreamConnectedMsg) (tea.Model, tea.Cmd) {
	return m, m.streams.OnNotificationConnected(msg.Ch)
}

func (m Model) handleNotificationEvent(msg uikit.NotificationEventMsg) (tea.Model, tea.Cmd) {
	switch msg.Event.Type {
	case "notification.created", "notification.updated":
		env, err := apiclient.DecodeNotificationEnvelope(msg.Event.Data)
		if err != nil {
			m.debugView.AppendLine("Failed to parse notification: " + err.Error())
		} else {
			m.notifications.SetUnread(int(env.UnreadCount))
			if m.notifications.Upsert(env.Notification) {
				m.updateLayout()
			}
		}
	case "notifications.unread_count_changed":
		count, err := apiclient.DecodeUnreadCountEnvelope(msg.Event.Data)
		if err != nil {
			m.debugView.AppendLine("Failed to parse unread count: " + err.Error())
		} else {
			m.notifications.SetUnread(int(count))
			m.updateLayout()
		}
	}
	return m, m.streams.ContinueListeningNotifications()
}

func (m Model) handleNotificationStreamDisconnected() (tea.Model, tea.Cmd) {
	m.debugView.AppendLine("Notifications stream disconnected. Reconnecting...")
	return m, m.streams.SubscribeNotifications()
}

func (m Model) handleNotificationUnreadCount(msg uikit.NotificationUnreadCountMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.debugView.AppendLine("Failed to load unread count: " + msg.Err.Error())
		return m, nil
	}
	m.notifications.SetUnread(int(msg.Count))
	m.updateLayout()
	return m, nil
}

func (m Model) handleNotificationsLoaded(msg uikit.NotificationsLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.debugView.AppendLine("Failed to load notifications: " + msg.Err.Error())
		return m, nil
	}
	if m.notifications.LoadHistorical(msg.Items) {
		m.updateLayout()
	}
	return m, nil
}

func (m Model) handleOpenRun(msg uikit.OpenRunMsg) (tea.Model, tea.Cmd) {
	if msg.Run == nil {
		return m, nil
	}
	return m, m.openExecView(msg.Run)
}

func (m Model) handleNotificationReadState(msg uikit.NotificationReadStateMsg) (tea.Model, tea.Cmd) {
	if msg.Err == nil {
		// Local state was already applied optimistically — nothing to do.
		// The server publishes notification.updated so other surfaces sync.
		return m, nil
	}
	verb := "read"
	if !msg.Read {
		verb = "unread"
	}
	m.debugView.AppendLine("Failed to mark notification " + verb + ": " + msg.Err.Error())
	// Roll back the optimistic update.
	if msg.Read {
		m.notifications.MarkUnreadLocal(msg.ID)
	} else {
		m.notifications.MarkReadLocal(msg.ID, time.Now())
	}
	m.updateLayout()
	return m, nil
}

// interceptConfirmDialog handles input while the confirm dialog is visible.
// Returns intercepted=false to let the main dispatcher process the message
// (e.g. WindowSizeMsg keeps layout responsive even with a dialog open).
func (m Model) interceptConfirmDialog(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	if m.dialogs.IsShuttingDown() {
		return m.interceptShuttingDownDialog(msg)
	}
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.String() == keyCtrlC {
			m.streams.Shutdown()
			m.quitAction = uikit.QuitKeepDaemon
			return m, tea.Quit, true
		}
		cmd, closed := m.dialogs.UpdateConfirmKeep(msg)
		if cmd != nil {
			return m, m.execConfirmCmd(cmd, closed), true
		}
		if closed {
			m.dialogs.DismissConfirm()
		}
		return m, nil, true
	case tea.MouseMsg:
		cmd, closed := m.dialogs.UpdateConfirmKeep(msg)
		if cmd != nil {
			return m, m.execConfirmCmd(cmd, closed), true
		}
		if closed {
			m.dialogs.DismissConfirm()
		}
		return m, nil, true
	}
	return m, nil, false
}

func (m Model) interceptShuttingDownDialog(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.String() == keyCtrlC {
			m.streams.Shutdown()
			m.quitAction = uikit.QuitKeepDaemon
			return m, tea.Quit, true
		}
		return m, nil, true
	case uikit.SpinnerTickMsg:
		cmd := m.dialogs.UpdateSpinner(msg.Inner)
		return m, cmd, true
	case uikit.ShutdownDoneMsg:
		m.shutdownErr = msg.Err
		return m, tea.Quit, true
	case tea.MouseMsg:
		return m, nil, true
	}
	return m, nil, false
}

// interceptParamFormDialog handles input while the parameter form is visible.
// The form captures all keys (it's a modal); a confirmed submit returns the
// trigger command, esc cancels, and ctrl+c escalates to the quit confirm.
func (m Model) interceptParamFormDialog(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.String() == keyCtrlC {
			m.dialogs.DismissParamForm()
			m.showQuitConfirm()
			return m, nil, true
		}
		cmd, _ := m.dialogs.UpdateParamForm(msg)
		return m, cmd, true
	case tea.MouseMsg:
		return m, nil, true
	}
	return m, nil, false
}

// interceptRunParamsDialog handles input while the read-only run-params modal
// is visible. Any close key dismisses it; ctrl+c escalates to the quit confirm.
// Mouse state is re-synced on close so terminal selection is re-enabled.
func (m Model) interceptRunParamsDialog(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.String() == keyCtrlC {
			m.dialogs.DismissRunParams()
			cmd := m.dialogs.SyncMouseState()
			m.showQuitConfirm()
			return m, cmd, true
		}
		if m.dialogs.UpdateRunParams(msg) {
			return m, m.dialogs.SyncMouseState(), true
		}
		return m, nil, true
	case tea.MouseMsg:
		if m.dialogs.UpdateRunParams(msg) {
			return m, m.dialogs.SyncMouseState(), true
		}
		return m, nil, true
	}
	return m, nil, false
}

// interceptCopyDialog handles input while the copy dialog is visible.
func (m Model) interceptCopyDialog(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.String() == keyCtrlC {
			m.dialogs.DismissCopy()
			cmd := m.dialogs.SyncMouseState()
			m.showQuitConfirm()
			return m, cmd, true
		}
		if m.dialogs.UpdateCopy(msg) {
			return m, m.dialogs.SyncMouseState(), true
		}
		return m, nil, true
	case tea.MouseMsg:
		if m.dialogs.UpdateCopy(msg) {
			return m, m.dialogs.SyncMouseState(), true
		}
		return m, nil, true
	}
	return m, nil, false
}

// interceptLogHistoryDialog handles input while the frame-history viewer is
// visible. Scroll keys are consumed by the dialog; any close key dismisses it;
// ctrl+c escalates to the quit confirm.
func (m Model) interceptLogHistoryDialog(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.String() == keyCtrlC {
			m.dialogs.DismissLogHistory()
			m.showQuitConfirm()
			return m, nil, true
		}
		m.dialogs.UpdateLogHistory(msg)
		return m, nil, true
	case tea.MouseMsg:
		m.dialogs.UpdateLogHistory(msg)
		return m, nil, true
	}
	return m, nil, false
}

// interceptTaskDetailDialog handles input while the task inspector is visible.
// Only key/mouse messages are consumed (so async TaskSummaryMsg fetches still
// reach the dispatchers and fill in the health line); ctrl+c escalates to quit.
func (m Model) interceptTaskDetailDialog(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.String() == keyCtrlC {
			m.dialogs.DismissTaskDetail()
			m.showQuitConfirm()
			return m, nil, true
		}
		m.dialogs.UpdateTaskDetail(msg)
		return m, nil, true
	case tea.MouseMsg:
		m.dialogs.UpdateTaskDetail(msg)
		return m, nil, true
	}
	return m, nil, false
}

// interceptRunDetailDialog handles input while the run inspector is visible.
// Enter opens the parent run when the displayed run is a retry; ctrl+c escalates
// to quit; any other close key dismisses it.
func (m Model) interceptRunDetailDialog(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		if mouse, isMouse := msg.(tea.MouseMsg); isMouse {
			m.dialogs.UpdateRunDetail(mouse)
			return m, nil, true
		}
		return m, nil, false
	}
	switch keyMsg.String() {
	case keyCtrlC:
		m.dialogs.DismissRunDetail()
		m.showQuitConfirm()
		return m, nil, true
	case "enter":
		if taskName, runID, hasParent := m.dialogs.RunDetailParent(); hasParent {
			m.dialogs.DismissRunDetail()
			return m, m.openRunByID(taskName, runID), true
		}
		return m, nil, true
	}
	m.dialogs.UpdateRunDetail(keyMsg)
	return m, nil, true
}

// interceptHelpDialog handles input while the help overlay is visible. The
// overlay is modal: close keys dismiss it, scroll keys move its viewport, and
// every other key/mouse event is swallowed so it never leaks to the background.
// ctrl+c escalates to the quit confirm.
func (m Model) interceptHelpDialog(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.String() == keyCtrlC {
			m.dialogs.DismissHelp()
			m.showQuitConfirm()
			return m, nil, true
		}
		m.dialogs.UpdateHelp(msg)
		return m, nil, true
	case tea.MouseMsg:
		m.dialogs.UpdateHelp(msg)
		return m, nil, true
	}
	return m, nil, false
}

func (m Model) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	m.ready = true
	m.updateLayout()
	return m, nil
}

func (m Model) handleExecWindowFetched(msg uikit.ExecWindowFetchedMsg) (tea.Model, tea.Cmd) {
	m.execWindow.ApplyFetch(msg.Items, msg.Offset, msg.Total)
	return m, nil
}

func (m Model) handleSSEConnected(msg uikit.SSEConnectedMsg) (tea.Model, tea.Cmd) {
	return m, m.streams.OnSSEConnected(msg.Ch)
}

func (m Model) handleSSEEventMsg(msg uikit.SSEEventMsg) (tea.Model, tea.Cmd) {
	cmd := m.handleSSEEvent(msg.Event)
	return m, tea.Batch(cmd, m.streams.ContinueListeningSSE())
}

func (m Model) handleSSEDisconnected() (tea.Model, tea.Cmd) {
	m.debugView.AppendLine("Events stream disconnected. Reconnecting...")
	return m, m.streams.SubscribeEvents()
}

// handleLogTailLoaded seeds the pane with the initial tail page in one Update
// (Follow snaps it to the bottom, so the first frame already shows the end),
// then opens the live stream for only the lines after the page. A finished run
// needs no live stream — the page holds everything.
func (m Model) handleLogTailLoaded(msg uikit.LogTailLoadedMsg) (tea.Model, tea.Cmd) {
	if !m.viewingRun(msg.RunID) {
		return m, nil
	}
	for _, l := range msg.Lines {
		m.execView.Pane.AppendLogLine(l.N, l.Stream, l.Text, l.FrameCount)
	}
	if n := len(msg.Lines); m.pendingHighlight != 0 && n > 0 && msg.Lines[n-1].N >= m.pendingHighlight {
		m.execView.Pane.JumpToLine(m.pendingHighlight)
		m.pendingHighlight = 0
	}
	if msg.Finalized {
		return m, nil
	}
	// Live stream anchor: the line after the last seeded one. The stream's own
	// disk backfill from this anchor closes the fetch↔subscribe race, and its
	// server-side dedupe drops anything already shown. An empty page falls back
	// to the tail anchor (new run, or a failed page fetch).
	from := int64(-execlist.LogTailLines)
	if n := len(msg.Lines); n > 0 {
		from = msg.Lines[n-1].N + 1
	}
	run := m.currentRun()
	if run == nil {
		return m, nil
	}
	return m, m.streams.StartLogStream(run, from)
}

func (m Model) handleLogStreamConnected(msg uikit.LogStreamConnectedMsg) (tea.Model, tea.Cmd) {
	if !m.viewingRun(msg.RunID) {
		return m, nil
	}
	return m, m.streams.OnLogConnected(msg.RunID, msg.Ch)
}

func (m Model) handleLogLine(msg uikit.LogLineMsg) (tea.Model, tea.Cmd) {
	if !m.viewingRun(msg.RunID) {
		return m, m.streams.ContinueListeningLog(msg.RunID)
	}
	m.execView.Pane.AppendLogLine(msg.Line.N, msg.Line.Stream, msg.Line.Text, msg.Line.FrameCount)
	// If a search hit selected this run, jump as soon as the target line
	// lands in the buffer. The pending marker is cleared so subsequent
	// scroll input isn't yanked back to the hit.
	if m.pendingHighlight != 0 && msg.Line.N >= m.pendingHighlight {
		m.execView.Pane.JumpToLine(m.pendingHighlight)
		m.pendingHighlight = 0
	}
	return m, m.streams.ContinueListeningLog(msg.RunID)
}

func (m Model) handleLogRegion(msg uikit.LogRegionMsg) (tea.Model, tea.Cmd) {
	if !m.viewingRun(msg.RunID) {
		return m, m.streams.ContinueListeningLog(msg.RunID)
	}
	m.execView.Pane.SetRegion(msg.Stream, msg.Rows)
	return m, m.streams.ContinueListeningLog(msg.RunID)
}

func (m Model) handleLogRotated(msg uikit.LogRotatedMsg) (tea.Model, tea.Cmd) {
	if !m.viewingRun(msg.RunID) {
		return m, m.streams.ContinueListeningLog(msg.RunID)
	}
	m.execView.Pane.EvictBelow(int(msg.FirstAvailable))
	return m, m.streams.ContinueListeningLog(msg.RunID)
}

func (m Model) handleLogDropped(msg uikit.LogDroppedMsg) (tea.Model, tea.Cmd) {
	if !m.viewingRun(msg.RunID) {
		return m, m.streams.ContinueListeningLog(msg.RunID)
	}
	m.debugView.AppendLine(fmt.Sprintf("Log stream dropped %d line(s) after #%d (server backpressure)", msg.Count, msg.After))
	return m, m.streams.ContinueListeningLog(msg.RunID)
}

func (m Model) handleLogDone(msg uikit.LogDoneMsg) (tea.Model, tea.Cmd) {
	if !m.viewingRun(msg.RunID) {
		return m, nil
	}
	if m.execView.Run.Status == model.PhaseRunning {
		return m, m.scheduleLogReconnect(msg.RunID)
	}
	// The run has ended: no live region remains, so drop any overlay that a
	// dropped clear-frame might have left painted.
	m.execView.Pane.ClearRegions()
	return m, nil
}

func (m Model) handleLogOlderLoaded(msg uikit.LogOlderLoadedMsg) (tea.Model, tea.Cmd) {
	if !m.viewingRun(msg.RunID) {
		return m, nil
	}
	m.execView.LoadingOlder = false
	pane := make([]logpane.Line, len(msg.Lines))
	for i, l := range msg.Lines {
		pane[i] = logpane.Line{Stream: l.Stream, Text: l.Text}
	}
	m.execView.Pane.PrependLines(pane, int(msg.FirstLine))
	if msg.Total > 0 {
		m.execView.Pane.SetTotalLines(int(msg.Total))
	}
	return m, nil
}

func (m Model) handleDebugLog(msg uikit.DebugLogMsg) (tea.Model, tea.Cmd) {
	m.debugView.AppendLine(msg.Message)
	return m, nil
}

func (m Model) handleOpenBrowser(msg uikit.OpenBrowserMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.debugView.AppendLine("Failed to open browser: " + msg.Err.Error())
	}
	if msg.BrowserOpened {
		return m, m.dialogs.Flash("Opened browser", 3*time.Second)
	}
	if msg.URL != "" {
		// No graphical session or browser failed — offer URL for manual copy.
		return m, m.dialogs.CopyToClipboard(msg.URL)
	}
	return m, nil
}

func (m Model) handleDaemonLogConnected(msg uikit.DaemonLogConnectedMsg) (tea.Model, tea.Cmd) {
	return m, m.streams.OnDaemonLogConnected(msg.Ch)
}

func (m Model) handleDaemonLogLine(msg uikit.DaemonLogLineMsg) (tea.Model, tea.Cmd) {
	m.debugView.AppendLine(msg.Line)
	return m, m.streams.ContinueListeningDaemonLog()
}

func (m Model) handleDaemonLogDisconnected() (tea.Model, tea.Cmd) {
	m.debugView.AppendLine("Daemon log stream disconnected. Reconnecting...")
	return m, m.streams.SubscribeDaemonLogs()
}

func (m Model) handleTriggerRun(msg uikit.TriggerRunMsg) (tea.Model, tea.Cmd) {
	action := "Triggered run for"
	if msg.Retry {
		action = "Retried run for"
	}
	m.logActionResult(action, msg.TaskName, msg.Err)
	if msg.Err != nil {
		// Concurrency limit or other error — close exec view to show the task list.
		if m.execView != nil {
			return m, tea.Batch(m.closeExecView(), m.dialogs.Flash("Run failed: "+msg.Err.Error(), 6*time.Second))
		}
		return m, m.dialogs.Flash("Run failed: "+msg.Err.Error(), 6*time.Second)
	}
	if msg.Run != nil {
		// Run/retry is confirmed up front (no undo toast); just flash the result.
		verb := "Started run for " + msg.TaskName
		if msg.Retry {
			verb = "Retried run for " + msg.TaskName
		}
		return m, tea.Batch(m.openExecView(msg.Run), m.dialogs.Flash(verb, 4*time.Second))
	}
	return m, nil
}

func (m Model) handleStopRun(msg uikit.StopRunMsg) (tea.Model, tea.Cmd) {
	m.logActionResult("Stopped run for", msg.TaskName, msg.Err)
	return m, nil
}

func (m Model) handleDeleteRun(msg uikit.DeleteRunMsg) (tea.Model, tea.Cmd) {
	m.logActionResult("Deleted run for", msg.TaskName, msg.Err)
	if msg.Err != nil {
		return m, m.dialogs.Flash("Delete failed: "+msg.Err.Error(), 6*time.Second)
	}
	var cmds []tea.Cmd
	if m.execView != nil && m.execView.Run != nil && m.execView.Run.ID == msg.RunID {
		if cmd := m.closeExecView(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	cmds = append(cmds, m.fetchExecWindow())
	// Soft delete is reversible — offer an undo that restores the run.
	undo := m.streams.RestoreRuns(model.RunSelector{IDs: []string{msg.RunID}})
	cmds = append(cmds, m.dialogs.FlashUndo("Deleted run — press u to undo", undo, 6*time.Second))
	return m, tea.Batch(cmds...)
}

// handleBulkAction applies the result of a bulk run operation (or its undo):
// refreshes the list and flashes a count. Bulk delete is itself undoable.
func (m Model) handleBulkAction(msg uikit.BulkActionMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		return m, m.dialogs.Flash(msg.Action+" failed: "+msg.Err.Error(), 6*time.Second)
	}
	cmds := []tea.Cmd{m.fetchExecWindow()}
	summary := fmt.Sprintf("%s %d run%s", msg.Action, msg.Affected, textutil.Pluralize(msg.Affected, "", "s"))
	cmds = append(cmds, m.dialogs.Flash(summary, 4*time.Second))
	return m, tea.Batch(cmds...)
}

// handleBulkDeleteResult refreshes the list after a bulk delete and offers an
// undo when the delete targeted explicit IDs (a precise restore). A MatchAll
// delete only flashes a count — restoring its filter could revive runs that were
// already soft-deleted before this action.
func (m Model) handleBulkDeleteResult(msg uikit.BulkDeleteResultMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		return m, m.dialogs.Flash("Delete failed: "+msg.Err.Error(), 6*time.Second)
	}
	cmds := []tea.Cmd{m.fetchExecWindow()}
	label := fmt.Sprintf("Deleted %d run%s", msg.Affected, textutil.Pluralize(msg.Affected, "", "s"))
	if !msg.Restore.MatchAll && msg.Affected > 0 {
		undo := m.streams.RestoreRuns(msg.Restore)
		cmds = append(cmds, m.dialogs.FlashUndo(label+" — press u to undo", undo, 6*time.Second))
	} else {
		cmds = append(cmds, m.dialogs.Flash(label, 4*time.Second))
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handleRestartService(msg uikit.RestartServiceMsg) (tea.Model, tea.Cmd) {
	m.logActionResult("Restarted service", msg.TaskName, msg.Err)
	if msg.Err == nil && m.execView != nil && m.execView.Run != nil && m.execView.Run.TaskName == msg.TaskName {
		m.execView.SetServiceStopped(false)
	}
	return m, nil
}

func (m Model) handleStopService(msg uikit.StopServiceMsg) (tea.Model, tea.Cmd) {
	m.logActionResult("Stopped service", msg.TaskName, msg.Err)
	if msg.Err == nil && m.execView != nil && m.execView.Run != nil && m.execView.Run.TaskName == msg.TaskName {
		m.execView.SetServiceStopped(true)
	}
	return m, nil
}

func (m Model) handleReconnectLog(msg uikit.ReconnectLogMsg) (tea.Model, tea.Cmd) {
	if !m.viewingRun(msg.RunID) || m.execView.Run.Status != model.PhaseRunning {
		return m, nil
	}
	resumeFrom := int64(m.execView.Pane.FirstLoadedLine + len(m.execView.Pane.Lines))
	return m, m.streams.StartLogStream(m.execView.Run, resumeFrom)
}

// handleLogLineHistory opens the frame-history viewer once the prior frames for
// an anchor line have been fetched. An error or empty result surfaces as a
// flash rather than an empty modal.
func (m Model) handleLogLineHistory(msg uikit.LogLineHistoryMsg) (tea.Model, tea.Cmd) {
	if !m.viewingRun(msg.RunID) {
		return m, nil
	}
	if msg.Err != nil {
		return m, m.dialogs.Flash("Failed to load frame history", 3*time.Second)
	}
	if len(msg.Frames) == 0 {
		return m, m.dialogs.Flash("No frame history for this line", 3*time.Second)
	}
	m.dialogs.ShowLogHistory(NewLogHistoryDialog(msg.Line, msg.Frames, msg.Committed))
	return m, nil
}

func (m Model) handleTick() (tea.Model, tea.Cmd) {
	m.execWindow.UpdateVisibleTimes(m.execList.Scroll, m.execList.ViewportHeight())
	cmds := []tea.Cmd{m.tickCmd()}
	if m.execList.NeedsFetch() {
		cmds = append(cmds, m.fetchExecWindow())
	}
	if m.sidebar.ActivePage() == uikit.PageInfo {
		cmds = append(cmds, m.streams.FetchSystemStats())
	}
	if time.Since(m.lastInfoFetch) >= infoPollInterval {
		m.lastInfoFetch = time.Now()
		cmds = append(cmds, m.streams.FetchDaemonInfo())
	}
	if m.notifications.PanelHeight() > 0 {
		m.notifications.RefreshLabels()
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handleQuit(msg uikit.QuitMsg) (tea.Model, tea.Cmd) {
	m.streams.Shutdown()
	m.quitAction = msg.Action
	if msg.Action == uikit.QuitShutdownDaemon && m.shutdownFunc != nil {
		return m, m.startShutdownSpinner()
	}
	return m, tea.Quit
}

func (m Model) handleFlashExpired() (tea.Model, tea.Cmd) {
	m.dialogs.ClearFlashIfExpired()
	return m, nil
}

func (m Model) handleSystemStats(msg uikit.SystemStatsMsg) (tea.Model, tea.Cmd) {
	if msg.Err == nil && msg.Stats != nil {
		m.infoView.UpdateStats(msg.Stats)
	}
	return m, nil
}

// handleDaemonInfo refreshes the live bits of StartupInfo from the periodic
// /api/info poll — currently the config-stale notice and the service-managed
// flag (the daemon may be restarted under a service manager mid-session).
// Errors are ignored: the header just keeps its last known state.
func (m Model) handleDaemonInfo(msg uikit.DaemonInfoMsg) (tea.Model, tea.Cmd) {
	if msg.Err == nil && msg.Info != nil {
		m.info.ConfigStale = msg.Info.ConfigStale
		m.info.ConfigWarnings = msg.Info.ConfigWarnings
		m.info.ServiceManaged = msg.Info.ServiceManaged
	}
	return m, nil
}

// reloadConfig triggers an explicit config reload from inside the TUI. The
// result arrives as a ReloadResultMsg.
func (m *Model) reloadConfig() tea.Cmd {
	if m.client == nil {
		return nil
	}
	return tea.Batch(m.dialogs.Flash("Reloading config…", 3*time.Second), m.streams.Reload())
}

// handleReloadResult applies an operator-triggered reload: on success it adopts
// the fresh task set, rebuilds the sidebar (preserving the active task), clears
// the config-stale notice, and flashes a summary; on failure it surfaces the
// daemon's reason and leaves the running set untouched.
func (m Model) handleReloadResult(msg uikit.ReloadResultMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		return m, m.dialogs.Flash("Reload failed: "+msg.Err.Error(), 6*time.Second)
	}

	var cmds []tea.Cmd
	if msg.Info != nil {
		m.info.Tasks = msg.Info.Tasks
		m.info.ConfigStale = msg.Info.ConfigStale
		m.info.ConfigWarnings = msg.Info.ConfigWarnings
		m.sidebar.Rebuild(msg.Info.Tasks)
		m.execList.SetFilter(m.sidebar.ActiveTask())
		m.recalcExecListHeight()
		m.updateLayout()
		cmds = append(cmds, m.fetchExecWindow())
	}
	cmds = append(cmds, m.dialogs.Flash(reloadSummary(msg.Result), 5*time.Second))
	return m, tea.Batch(cmds...)
}

// reloadSummary renders a one-line summary of what a reload changed.
func reloadSummary(r *model.ReloadResult) string {
	if r == nil || r.IsEmpty() {
		return "✓ Config reloaded — no changes"
	}
	var parts []string
	if n := len(r.Added); n > 0 {
		parts = append(parts, fmt.Sprintf("+%d added", n))
	}
	if n := len(r.Removed); n > 0 {
		parts = append(parts, fmt.Sprintf("-%d removed", n))
	}
	if n := len(r.Changed); n > 0 {
		parts = append(parts, fmt.Sprintf("~%d changed", n))
	}
	return "✓ Config reloaded: " + strings.Join(parts, ", ")
}

func (m Model) handleMetricsHistory(msg uikit.MetricsHistoryMsg) (tea.Model, tea.Cmd) {
	if msg.Err == nil && msg.Samples != nil {
		m.infoView.LoadHistory(msg.Samples)
	}
	return m, nil
}

func (m Model) handleRunSummary(msg uikit.RunSummaryMsg) (tea.Model, tea.Cmd) {
	if msg.Err == nil && msg.Summary != nil {
		m.infoView.UpdateRunSummary(msg.Summary)
	}
	return m, nil
}

// viewingRun reports whether the active execView is showing the given run ID.
func (m *Model) viewingRun(runID string) bool {
	return m.execView != nil && m.execView.RunID() == runID
}

// maybeLoadOlderLogs checks if the user scrolled to the top of the loaded buffer
// and dispatches a fetch for older lines if available.
func (m *Model) maybeLoadOlderLogs() tea.Cmd {
	if m.execView == nil || m.execView.LoadingOlder {
		return nil
	}
	if !m.execView.Pane.NeedsOlder() {
		return nil
	}
	m.execView.LoadingOlder = true
	return m.streams.FetchOlderLogs(
		m.execView.Run.TaskName,
		m.execView.Run.ID,
		int64(m.execView.Pane.FirstLoadedLineNum()),
		int64(execlist.LogTailLines),
	)
}

// handleSSEEvent processes a parsed SSE run event.
func (m *Model) handleSSEEvent(evt apiclient.RunStreamEvent) tea.Cmd {
	var runEvt struct {
		Run      *model.Run     `json:"run"`
		TaskName string         `json:"taskName"`
		Status   model.RunPhase `json:"status"`
	}
	if err := json.Unmarshal(evt.Data, &runEvt); err != nil {
		m.debugView.AppendLine("Failed to parse event: " + err.Error())
		return nil
	}

	if runEvt.Run != nil {
		m.execWindow.UpsertRun(*runEvt.Run)

		// Update exec view if watching this run.
		if m.execView != nil && m.execView.RunID() == runEvt.Run.ID {
			prevStatus := m.execView.Run.Status
			m.execView.Run = runEvt.Run
			if runEvt.Run.Status == model.PhaseRunning && prevStatus != model.PhaseRunning {
				return m.streams.StartLogStream(runEvt.Run, -int64(execlist.LogTailLines))
			}
		}

		// Auto-open when a single-instance service starts and user is viewing that task.
		if m.execView == nil && runEvt.Run.Status == model.PhaseRunning &&
			m.sidebar.ActiveTask() == runEvt.TaskName &&
			m.isSingleInstanceService(runEvt.TaskName) {
			return m.openExecView(runEvt.Run)
		}
	}

	return nil
}

const logReconnectDelay = 500 * time.Millisecond

// execConfirmCmd runs a confirm-dialog callback synchronously and returns the
// appropriate tea.Cmd. For quit callbacks this handles the quit inline —
// calling Shutdown and returning tea.Quit directly — which prevents the quit
// message from being starved by mouse-motion events that flood BubbleTea's
// unbuffered message channel when WithMouseAllMotion is enabled.
//
// closed indicates whether the dialog signalled it should close. When the
// callback triggers a shutdown we keep the dialog open and transition it into
// the spinner state; otherwise we dismiss it.
func (m *Model) execConfirmCmd(cmd tea.Cmd, closed bool) tea.Cmd {
	result := cmd()
	if result == nil {
		if closed {
			m.dialogs.DismissConfirm()
		}
		return nil
	}
	if qm, ok := result.(uikit.QuitMsg); ok {
		m.streams.Shutdown()
		m.quitAction = qm.Action
		if qm.Action == uikit.QuitShutdownDaemon && m.shutdownFunc != nil {
			return m.startShutdownSpinner()
		}
		m.dialogs.DismissConfirm()
		return tea.Quit
	}
	if closed {
		m.dialogs.DismissConfirm()
	}
	return func() tea.Msg { return result }
}

// startShutdownSpinner transitions the confirm dialog into the shutting-down
// spinner state and fires the shutdown function as a background command.
// If no dialog is open (e.g. uikit.QuitMsg arrived without a confirm), a new one is
// created so the spinner has somewhere to render.
func (m *Model) startShutdownSpinner() tea.Cmd {
	if !m.dialogs.HasConfirm() {
		dialog := NewConfirmDialog("Quit", "", nil)
		m.dialogs.ShowConfirm(dialog)
	}
	spinnerCmd := m.dialogs.StartShutdown()
	shutdownFn := m.shutdownFunc
	shutdownCmd := func() tea.Msg {
		err := shutdownFn()
		return uikit.ShutdownDoneMsg{Err: err}
	}
	return tea.Batch(tea.ClearScreen, spinnerCmd, shutdownCmd)
}

// scheduleLogReconnect returns a delayed command to reconnect the log stream.
// Used when a stream ends while the run is still running (e.g. server-side
// timeout or transient disconnect).
func (m *Model) scheduleLogReconnect(runID string) tea.Cmd {
	return tea.Tick(logReconnectDelay, func(time.Time) tea.Msg {
		return uikit.ReconnectLogMsg{RunID: runID}
	})
}
