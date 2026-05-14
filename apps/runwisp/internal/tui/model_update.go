// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"encoding/json"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/execlist"
	"github.com/runwisp/runwisp/internal/tui/views/logpane"
)

// Update processes messages by delegating to per-type handlers.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.dialogs.HasConfirm() {
		if newModel, cmd, intercepted := m.interceptConfirmDialog(msg); intercepted {
			return newModel, cmd
		}
	}
	if m.dialogs.HasCopy() {
		if newModel, cmd, intercepted := m.interceptCopyDialog(msg); intercepted {
			return newModel, cmd
		}
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg)
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case tea.KeyMsg:
		return m.handleKey(msg)
	case uikit.ExecWindowFetchedMsg:
		return m.handleExecWindowFetched(msg)
	case uikit.SSEConnectedMsg:
		return m.handleSSEConnected(msg)
	case uikit.SSEEventMsg:
		return m.handleSSEEventMsg(msg)
	case uikit.SSEDisconnectedMsg:
		return m.handleSSEDisconnected()
	case uikit.LogOlderLoadedMsg:
		return m.handleLogOlderLoaded(msg)
	case uikit.LogStreamConnectedMsg:
		return m.handleLogStreamConnected(msg)
	case uikit.LogLineMsg:
		return m.handleLogLine(msg)
	case uikit.LogRotatedMsg:
		return m.handleLogRotated(msg)
	case uikit.LogDroppedMsg:
		return m.handleLogDropped(msg)
	case uikit.LogDoneMsg:
		return m.handleLogDone(msg)
	case uikit.DebugLogMsg:
		return m.handleDebugLog(msg)
	case uikit.OpenBrowserMsg:
		return m.handleOpenBrowser(msg)
	case uikit.DaemonLogConnectedMsg:
		return m.handleDaemonLogConnected(msg)
	case uikit.DaemonLogLineMsg:
		return m.handleDaemonLogLine(msg)
	case uikit.DaemonLogDisconnectedMsg:
		return m.handleDaemonLogDisconnected()
	case uikit.TriggerRunMsg:
		return m.handleTriggerRun(msg)
	case uikit.StopRunMsg:
		return m.handleStopRun(msg)
	case uikit.RestartServiceMsg:
		return m.handleRestartService(msg)
	case uikit.StopServiceMsg:
		return m.handleStopService(msg)
	case uikit.ReconnectLogMsg:
		return m.handleReconnectLog(msg)
	case uikit.TickMsg:
		return m.handleTick()
	case uikit.QuitMsg:
		return m.handleQuit(msg)
	case uikit.FlashExpiredMsg:
		return m.handleFlashExpired()
	case uikit.SystemStatsMsg:
		return m.handleSystemStats(msg)
	case uikit.MetricsHistoryMsg:
		return m.handleMetricsHistory(msg)
	case uikit.RunSummaryMsg:
		return m.handleRunSummary(msg)
	case uikit.NotificationStreamConnectedMsg:
		return m.handleNotificationStreamConnected(msg)
	case uikit.NotificationEventMsg:
		return m.handleNotificationEvent(msg)
	case uikit.NotificationStreamDisconnectedMsg:
		return m.handleNotificationStreamDisconnected()
	case uikit.NotificationUnreadCountMsg:
		return m.handleNotificationUnreadCount(msg)
	case uikit.NotificationsLoadedMsg:
		return m.handleNotificationsLoaded(msg)
	case uikit.NotificationReadStateMsg:
		return m.handleNotificationReadState(msg)
	case uikit.NotificationBoundaryFlashClearedMsg:
		m.notifications.ClearBoundaryFlash()
		return m, nil
	case uikit.OpenRunMsg:
		return m.handleOpenRun(msg)
	}
	return m, nil
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
		switch msg := msg.(type) {
		case tea.KeyMsg:
			if msg.String() == "ctrl+c" {
				m.streams.Shutdown()
				m.quitAction = uikit.QuitKeepDaemon
				return m, tea.Quit, true
			}
			return m, nil, true
		case uikit.SpinnerTickMsg:
			cmd := m.dialogs.UpdateSpinner(msg.Inner)
			return m, cmd, true
		case uikit.ShutdownDoneMsg:
			return m, tea.Quit, true
		case tea.MouseMsg:
			return m, nil, true
		}
		return m, nil, false
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
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

// interceptCopyDialog handles input while the copy dialog is visible.
func (m Model) interceptCopyDialog(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
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
	m.execView.Pane.AppendLine(msg.Line.N, msg.Line.Stream, msg.Line.Text)
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
			return m, m.closeExecView()
		}
		return m, nil
	}
	if msg.Run != nil {
		return m, m.openExecView(msg.Run)
	}
	return m, nil
}

func (m Model) handleStopRun(msg uikit.StopRunMsg) (tea.Model, tea.Cmd) {
	m.logActionResult("Stopped run for", msg.TaskName, msg.Err)
	return m, nil
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

func (m Model) handleTick() (tea.Model, tea.Cmd) {
	m.execWindow.UpdateVisibleTimes(m.execList.Scroll, m.execList.ViewportHeight())
	cmds := []tea.Cmd{m.tickCmd()}
	if m.execList.NeedsFetch() {
		cmds = append(cmds, m.fetchExecWindow())
	}
	if m.sidebar.ActivePage() == uikit.PageInfo {
		cmds = append(cmds, m.streams.FetchSystemStats())
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
		TaskName string         `json:"task_name"`
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
