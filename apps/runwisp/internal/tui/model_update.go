// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"encoding/json"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
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
	case execWindowFetchedMsg:
		return m.handleExecWindowFetched(msg)
	case sseConnectedMsg:
		return m.handleSSEConnected(msg)
	case sseEventMsg:
		return m.handleSSEEventMsg(msg)
	case sseDisconnectedMsg:
		return m.handleSSEDisconnected()
	case logStreamConnectedMsg:
		return m.handleLogStreamConnected(msg)
	case logChunkMsg:
		return m.handleLogChunk(msg)
	case logDoneMsg:
		return m.handleLogDone(msg)
	case logBatchMsg:
		return m.handleLogBatch(msg)
	case DebugLogMsg:
		return m.handleDebugLog(msg)
	case openBrowserMsg:
		return m.handleOpenBrowser(msg)
	case daemonLogConnectedMsg:
		return m.handleDaemonLogConnected(msg)
	case daemonLogLineMsg:
		return m.handleDaemonLogLine(msg)
	case daemonLogDisconnectedMsg:
		return m.handleDaemonLogDisconnected()
	case TriggerRunMsg:
		return m.handleTriggerRun(msg)
	case StopRunMsg:
		return m.handleStopRun(msg)
	case RestartServiceMsg:
		return m.handleRestartService(msg)
	case reconnectLogMsg:
		return m.handleReconnectLog(msg)
	case TickMsg:
		return m.handleTick()
	case quitMsg:
		return m.handleQuit(msg)
	case flashExpiredMsg:
		return m.handleFlashExpired()
	case systemStatsMsg:
		return m.handleSystemStats(msg)
	case metricsHistoryMsg:
		return m.handleMetricsHistory(msg)
	case runSummaryMsg:
		return m.handleRunSummary(msg)
	case notificationStreamConnectedMsg:
		return m.handleNotificationStreamConnected(msg)
	case notificationEventMsg:
		return m.handleNotificationEvent(msg)
	case notificationStreamDisconnectedMsg:
		return m.handleNotificationStreamDisconnected()
	case notificationUnreadCountMsg:
		return m.handleNotificationUnreadCount(msg)
	case notificationsLoadedMsg:
		return m.handleNotificationsLoaded(msg)
	case notificationReadStateMsg:
		return m.handleNotificationReadState(msg)
	case notificationBoundaryFlashClearedMsg:
		m.notifications.ClearBoundaryFlash()
		return m, nil
	case openRunMsg:
		return m.handleOpenRun(msg)
	}
	return m, nil
}

func (m Model) handleNotificationStreamConnected(msg notificationStreamConnectedMsg) (tea.Model, tea.Cmd) {
	return m, m.streams.OnNotificationConnected(msg.ch)
}

func (m Model) handleNotificationEvent(msg notificationEventMsg) (tea.Model, tea.Cmd) {
	switch msg.Event.Type {
	case "notification.created", "notification.updated":
		n, err := apiclient.DecodeNotificationEnvelope(msg.Event.Data)
		if err != nil {
			m.debugView.AppendLine("Failed to parse notification: " + err.Error())
		} else if m.notifications.Upsert(n) {
			m.updateLayout()
		}
	}
	return m, m.streams.ContinueListeningNotifications()
}

func (m Model) handleNotificationStreamDisconnected() (tea.Model, tea.Cmd) {
	m.debugView.AppendLine("Notifications stream disconnected. Reconnecting...")
	return m, m.streams.SubscribeNotifications()
}

func (m Model) handleNotificationUnreadCount(msg notificationUnreadCountMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.debugView.AppendLine("Failed to load unread count: " + msg.Err.Error())
		return m, nil
	}
	m.notifications.SetUnreadHint(int(msg.Count))
	m.updateLayout()
	return m, nil
}

func (m Model) handleNotificationsLoaded(msg notificationsLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.debugView.AppendLine("Failed to load notifications: " + msg.Err.Error())
		return m, nil
	}
	if m.notifications.LoadHistorical(msg.Items) {
		m.updateLayout()
	}
	return m, nil
}

func (m Model) handleOpenRun(msg openRunMsg) (tea.Model, tea.Cmd) {
	if msg.Run == nil {
		return m, nil
	}
	return m, m.openExecView(msg.Run)
}

func (m Model) handleNotificationReadState(msg notificationReadStateMsg) (tea.Model, tea.Cmd) {
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
				m.quitAction = QuitKeepDaemon
				return m, tea.Quit, true
			}
			return m, nil, true
		case spinnerTickMsg:
			cmd := m.dialogs.UpdateSpinner(msg.Inner)
			return m, cmd, true
		case shutdownDoneMsg:
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
			m.quitAction = QuitKeepDaemon
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

func (m Model) handleExecWindowFetched(msg execWindowFetchedMsg) (tea.Model, tea.Cmd) {
	m.execWindow.ApplyFetch(msg.Items, msg.Offset, msg.Total)
	return m, nil
}

func (m Model) handleSSEConnected(msg sseConnectedMsg) (tea.Model, tea.Cmd) {
	return m, m.streams.OnSSEConnected(msg.ch)
}

func (m Model) handleSSEEventMsg(msg sseEventMsg) (tea.Model, tea.Cmd) {
	cmd := m.handleSSEEvent(msg.Event)
	return m, tea.Batch(cmd, m.streams.ContinueListeningSSE())
}

func (m Model) handleSSEDisconnected() (tea.Model, tea.Cmd) {
	m.debugView.AppendLine("Events stream disconnected. Reconnecting...")
	return m, m.streams.SubscribeEvents()
}

func (m Model) handleLogStreamConnected(msg logStreamConnectedMsg) (tea.Model, tea.Cmd) {
	if !m.viewingRun(msg.RunID) {
		return m, nil
	}
	return m, m.streams.OnLogConnected(msg.RunID, msg.Ch)
}

func (m Model) handleLogChunk(msg logChunkMsg) (tea.Model, tea.Cmd) {
	if !m.viewingRun(msg.RunID) {
		return m, nil
	}
	m.execView.AppendChunk(msg.Chunk)
	return m, m.streams.ContinueListeningLog(msg.RunID)
}

func (m Model) handleLogDone(msg logDoneMsg) (tea.Model, tea.Cmd) {
	if !m.viewingRun(msg.RunID) {
		return m, nil
	}
	m.execView.FlushPending()
	if m.execView.run.Status == model.PhaseRunning {
		return m, m.scheduleLogReconnect(msg.RunID)
	}
	return m, nil
}

func (m Model) handleLogBatch(msg logBatchMsg) (tea.Model, tea.Cmd) {
	if !m.viewingRun(msg.RunID) {
		return m, nil
	}
	for _, line := range msg.Lines {
		m.execView.AppendChunk(line + "\n")
	}
	m.execView.FlushPending()
	if m.execView.run.Status == model.PhaseRunning {
		return m, m.scheduleLogReconnect(msg.RunID)
	}
	return m, nil
}

func (m Model) handleDebugLog(msg DebugLogMsg) (tea.Model, tea.Cmd) {
	m.debugView.AppendLine(msg.Message)
	return m, nil
}

func (m Model) handleOpenBrowser(msg openBrowserMsg) (tea.Model, tea.Cmd) {
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

func (m Model) handleDaemonLogConnected(msg daemonLogConnectedMsg) (tea.Model, tea.Cmd) {
	return m, m.streams.OnDaemonLogConnected(msg.ch)
}

func (m Model) handleDaemonLogLine(msg daemonLogLineMsg) (tea.Model, tea.Cmd) {
	m.debugView.AppendLine(msg.Line)
	return m, m.streams.ContinueListeningDaemonLog()
}

func (m Model) handleDaemonLogDisconnected() (tea.Model, tea.Cmd) {
	m.debugView.AppendLine("Daemon log stream disconnected. Reconnecting...")
	return m, m.streams.SubscribeDaemonLogs()
}

func (m Model) handleTriggerRun(msg TriggerRunMsg) (tea.Model, tea.Cmd) {
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

func (m Model) handleStopRun(msg StopRunMsg) (tea.Model, tea.Cmd) {
	m.logActionResult("Stopped run for", msg.TaskName, msg.Err)
	return m, nil
}

func (m Model) handleRestartService(msg RestartServiceMsg) (tea.Model, tea.Cmd) {
	m.logActionResult("Restarted service", msg.TaskName, msg.Err)
	return m, nil
}

func (m Model) handleReconnectLog(msg reconnectLogMsg) (tea.Model, tea.Cmd) {
	if !m.viewingRun(msg.RunID) || m.execView.run.Status != model.PhaseRunning {
		return m, nil
	}
	return m, m.streams.StartLogStream(m.execView.run)
}

func (m Model) handleTick() (tea.Model, tea.Cmd) {
	m.execWindow.UpdateVisibleTimes(m.execList.scroll, m.execList.viewportHeight())
	cmds := []tea.Cmd{m.tickCmd()}
	if m.execList.NeedsFetch() {
		cmds = append(cmds, m.fetchExecWindow())
	}
	if m.sidebar.ActivePage() == PageInfo {
		cmds = append(cmds, m.streams.FetchSystemStats())
	}
	if m.notifications.PanelHeight() > 0 {
		m.notifications.RefreshLabels()
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handleQuit(msg quitMsg) (tea.Model, tea.Cmd) {
	m.streams.Shutdown()
	m.quitAction = msg.Action
	if msg.Action == QuitShutdownDaemon && m.shutdownFunc != nil {
		return m, m.startShutdownSpinner()
	}
	return m, tea.Quit
}

func (m Model) handleFlashExpired() (tea.Model, tea.Cmd) {
	m.dialogs.ClearFlashIfExpired()
	return m, nil
}

func (m Model) handleSystemStats(msg systemStatsMsg) (tea.Model, tea.Cmd) {
	if msg.Err == nil && msg.Stats != nil {
		m.infoView.UpdateStats(msg.Stats)
	}
	return m, nil
}

func (m Model) handleMetricsHistory(msg metricsHistoryMsg) (tea.Model, tea.Cmd) {
	if msg.Err == nil && msg.Samples != nil {
		m.infoView.LoadHistory(msg.Samples)
	}
	return m, nil
}

func (m Model) handleRunSummary(msg runSummaryMsg) (tea.Model, tea.Cmd) {
	if msg.Err == nil && msg.Summary != nil {
		m.infoView.UpdateRunSummary(msg.Summary)
	}
	return m, nil
}

// viewingRun reports whether the active execView is showing the given run ID.
func (m *Model) viewingRun(runID string) bool {
	return m.execView != nil && m.execView.RunID() == runID
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
			prevStatus := m.execView.run.Status
			m.execView.run = runEvt.Run
			if runEvt.Run.Status == model.PhaseRunning && prevStatus != model.PhaseRunning {
				return m.streams.StartLogStream(runEvt.Run)
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
	if qm, ok := result.(quitMsg); ok {
		m.streams.Shutdown()
		m.quitAction = qm.Action
		if qm.Action == QuitShutdownDaemon && m.shutdownFunc != nil {
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
// If no dialog is open (e.g. quitMsg arrived without a confirm), a new one is
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
		return shutdownDoneMsg{Err: err}
	}
	return tea.Batch(tea.ClearScreen, spinnerCmd, shutdownCmd)
}

// scheduleLogReconnect returns a delayed command to reconnect the log stream.
// Used when a stream ends while the run is still running (e.g. server-side
// timeout or transient disconnect).
func (m *Model) scheduleLogReconnect(runID string) tea.Cmd {
	return tea.Tick(logReconnectDelay, func(time.Time) tea.Msg {
		return reconnectLogMsg{RunID: runID}
	})
}
