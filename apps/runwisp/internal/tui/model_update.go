// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server"
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
	// Reset the per-message frame-reuse flag; only a coalesced mouse motion/wheel
	// (see handleMouse) sets it back on, so every other message rebuilds the view
	// and no update is ever suppressed beyond one coalesce tick.
	m.coalesce = false

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

// handled adapts a handler's (model, cmd) result to a dispatcher's
// "claimed" return: `return handled(m.handleX(msg))`.
func handled(model tea.Model, cmd tea.Cmd) (tea.Model, tea.Cmd, bool) {
	return model, cmd, true
}

// interceptActiveDialog gives the topmost open dialog first claim on the
// message. Dialogs consume only key and mouse input (plus the shutdown
// spinner's own messages); everything else falls through to the dispatchers,
// e.g. WindowSizeMsg keeps layout responsive and an async TaskSummaryMsg still
// fills in the open task inspector.
//
// A dialog's command (e.g. a confirm callback) is returned, never invoked here:
// most make a network call, and running one inline would freeze the Update
// loop for as long as it takes.
func (m Model) interceptActiveDialog(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	kind, d, ok := m.dialogs.top()
	if !ok {
		return m, nil, false
	}
	if m.dialogs.IsShuttingDown() {
		return m.interceptShuttingDownDialog(msg)
	}
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case keyCtrlC:
			if kind == dlgConfirm {
				m.streams.Shutdown()
				m.quitAction = uikit.QuitKeepDaemon
				return m, tea.Quit, true
			}
			// In any other dialog, ctrl+c closes it and escalates to the quit confirm.
			m.dialogs.Dismiss(kind)
			return m, tea.Batch(m.dialogs.SyncMouseState(), m.requestQuit()), true
		case "enter":
			// Enter on a retry's inspector opens the run it retried.
			if rd, isRun := d.(*RunDetailDialog); isRun {
				if taskName, runID, hasParent := rd.ParentRef(); hasParent {
					m.dialogs.Dismiss(dlgRunDetail)
					return m, m.openRunByID(taskName, runID), true
				}
			}
		}
	case tea.MouseMsg:
	default:
		return m, nil, false
	}
	cmd, closed := d.Update(msg)
	if closed {
		m.dialogs.Dismiss(kind)
	}
	return m, tea.Batch(cmd, m.dialogs.SyncMouseState()), true
}

func (m Model) dispatchInputMsg(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return handled(m.handleWindowSize(msg))
	case tea.MouseMsg:
		return handled(m.handleMouse(msg))
	case tea.KeyPressMsg:
		return handled(m.handleKey(msg))
	}
	return m, nil, false
}

func (m Model) dispatchStreamMsg(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case uikit.ExecWindowFetchedMsg:
		return handled(m.handleExecWindowFetched(msg))
	case uikit.SSEConnectedMsg:
		return handled(m.handleSSEConnected(msg))
	case uikit.SSEEventMsg:
		return handled(m.handleSSEEventMsg(msg))
	case uikit.SSEDisconnectedMsg:
		return handled(m.handleSSEDisconnected(msg))
	}
	return m, nil, false
}

func (m Model) dispatchLogMsg(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case uikit.LogOlderLoadedMsg:
		return handled(m.handleLogOlderLoaded(msg))
	case uikit.LogTailLoadedMsg:
		return handled(m.handleLogTailLoaded(msg))
	case uikit.LogStreamConnectedMsg:
		return handled(m.handleLogStreamConnected(msg))
	case uikit.LogLineMsg:
		return handled(m.handleLogLine(msg))
	case uikit.LogRegionMsg:
		return handled(m.handleLogRegion(msg))
	case uikit.LogRotatedMsg:
		return handled(m.handleLogRotated(msg))
	case uikit.LogDroppedMsg:
		return handled(m.handleLogDropped(msg))
	case uikit.LogDoneMsg:
		return handled(m.handleLogDone(msg))
	case uikit.DebugLogMsg:
		return handled(m.handleDebugLog(msg))
	case uikit.ReconnectLogMsg:
		return handled(m.handleReconnectLog(msg))
	case uikit.LogLineHistoryMsg:
		return handled(m.handleLogLineHistory(msg))
	case uikit.DaemonLogConnectedMsg:
		return handled(m.handleDaemonLogConnected(msg))
	case uikit.DaemonLogLineMsg:
		return handled(m.handleDaemonLogLine(msg))
	case uikit.DaemonLogDisconnectedMsg:
		return handled(m.handleDaemonLogDisconnected())
	}
	return m, nil, false
}

func (m Model) dispatchNotificationMsg(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case uikit.NotificationUnreadCountMsg:
		return handled(m.handleNotificationUnreadCount(msg))
	case uikit.NotificationsLoadedMsg:
		return handled(m.handleNotificationsLoaded(msg))
	case uikit.NotificationReadStateMsg:
		return handled(m.handleNotificationReadState(msg))
	case uikit.NotificationBoundaryFlashClearedMsg:
		m.notifications.ClearBoundaryFlash()
		return m, nil, true
	}
	return m, nil, false
}

func (m Model) dispatchActionMsg(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case uikit.TriggerRunMsg:
		return handled(m.handleTriggerRun(msg))
	case uikit.StopRunMsg:
		return handled(m.handleStopRun(msg))
	case uikit.RestartServiceMsg:
		return handled(m.handleRestartService(msg))
	case uikit.StopServiceMsg:
		return handled(m.handleStopService(msg))
	case uikit.DeleteRunMsg:
		return handled(m.handleDeleteRun(msg))
	case uikit.SchedulePauseMsg:
		return handled(m.handleSchedulePause(msg))
	case uikit.TaskStateMsg:
		if msg.Err == nil {
			m.info.PausedTasks = msg.Paused
			m.info.TaskUsage = msg.Usage
			m.infoView.SetTaskUsage(msg.Usage)
			m.info.StoppedServices = msg.Stopped
			if m.execView != nil && m.execView.Run != nil {
				m.execView.SetServiceStopped(msg.Stopped[m.execView.Run.TaskName])
			}
		}
		return m, nil, true
	case uikit.BulkActionMsg:
		return handled(m.handleBulkAction(msg))
	case uikit.BulkDeleteResultMsg:
		return handled(m.handleBulkDeleteResult(msg))
	}
	return m, nil, false
}

func (m Model) dispatchLifecycleMsg(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case logsearch.SelectMsg:
		return handled(m.handleLogSearchSelect(msg))
	case uikit.TickMsg:
		return handled(m.handleTick())
	case coalesceFlushMsg:
		// The coalesce window elapsed; the reset at the top of Update already
		// cleared m.coalesce, so this frame rebuilds fresh. Record it as the last
		// real frame so the next event paces from here.
		m.flushPending = false
		m.lastRenderAt = time.Now()
		return m, nil, true
	case uikit.QuitMsg:
		return handled(m.handleQuit(msg))
	case uikit.FlashExpiredMsg:
		return handled(m.handleFlashExpired())
	case uikit.OpenBrowserMsg:
		return handled(m.handleOpenBrowser(msg))
	case uikit.OpenRunMsg:
		return handled(m.handleOpenRun(msg))
	case uikit.SystemStatsMsg:
		return handled(m.handleSystemStats(msg))
	case uikit.DaemonInfoMsg:
		return handled(m.handleDaemonInfo(msg))
	case uikit.ReloadResultMsg:
		return handled(m.handleReloadResult(msg))
	case uikit.MetricsHistoryMsg:
		return handled(m.handleMetricsHistory(msg))
	case uikit.RunSummaryMsg:
		return handled(m.handleRunSummary(msg))
	case uikit.TaskSummaryMsg:
		m.dialogs.ApplyTaskSummary(msg)
		return m, nil, true
	}
	return m, nil, false
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
	if msg.Err != nil {
		m.debugView.AppendLine(fmt.Sprintf("Failed to load run %s: %s", msg.RunID, msg.Err))
		if apiclient.IsHTTPStatus(msg.Err, http.StatusNotFound) {
			// Nothing left to open: count the attempt as acknowledging any
			// notification that still points at the run.
			return m, tea.Batch(
				m.markRunNotificationsRead(msg.RunID),
				m.dialogs.FlashError("That run no longer exists (deleted or cleaned up by retention)", 6*time.Second),
			)
		}
		return m, m.dialogs.FlashError("Couldn't open run: "+msg.Err.Error(), 6*time.Second)
	}
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

// interceptShuttingDownDialog handles input while the quit confirm shows its
// shutdown spinner: ctrl+c quits without waiting, the spinner's own messages
// drive it, and all other key/mouse input is swallowed.
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

func (m Model) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	m.ready = true
	m.updateLayout()
	return m, nil
}

func (m Model) handleExecWindowFetched(msg uikit.ExecWindowFetchedMsg) (tea.Model, tea.Cmd) {
	if !m.execWindow.IsCurrent(msg.Gen) {
		return m, nil
	}
	m.execWindow.ApplyFetch(msg.Items, msg.Offset, msg.Total)
	return m, nil
}

func (m Model) handleSSEConnected(msg uikit.SSEConnectedMsg) (tea.Model, tea.Cmd) {
	return m, m.streams.OnSSEConnected(msg.Ch)
}

func (m Model) handleSSEEventMsg(msg uikit.SSEEventMsg) (tea.Model, tea.Cmd) {
	m.streams.RecordEventID(msg.Event.ID)
	cmd := m.handleSSEEvent(msg.Event)
	return m, tea.Batch(cmd, m.streams.ContinueListeningSSE())
}

func (m Model) handleSSEDisconnected(msg uikit.SSEDisconnectedMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		m.debugView.AppendLine("Events stream failed: " + msg.Err.Error() + ". Retrying...")
	} else {
		m.debugView.AppendLine("Events stream disconnected. Reconnecting...")
	}
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
	if n := len(msg.Lines); m.pendingHighlight != 0 && m.pendingHighlightRun == msg.RunID && n > 0 && msg.Lines[n-1].N+1 >= m.pendingHighlight {
		m.execView.Pane.JumpToLine(m.pendingHighlight)
		m.pendingHighlight = 0
		m.pendingHighlightRun = ""
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
	if m.pendingHighlight != 0 && m.pendingHighlightRun == msg.RunID && msg.Line.N+1 >= m.pendingHighlight {
		m.execView.Pane.JumpToLine(m.pendingHighlight)
		m.pendingHighlight = 0
		m.pendingHighlightRun = ""
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
	m.execView.Pane.SetFirstAvailable(int(msg.FirstAvailable))
	// On a rotated log the server clamps `from` up to FirstAvailable but keeps the
	// requested limit, so the page can run past the lines already loaded. Keep only
	// the lines below the loaded range, and anchor on the first one returned.
	older := msg.Lines
	for len(older) > 0 && older[len(older)-1].N >= int64(m.execView.Pane.FirstLoadedLineNum()) {
		older = older[:len(older)-1]
	}
	if len(older) > 0 {
		pane := make([]logpane.Line, len(older))
		for i, l := range older {
			pane[i] = logpane.Line{Stream: l.Stream, Text: l.Text}
		}
		m.execView.Pane.PrependLines(pane, int(older[0].N))
	}
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
			return m, tea.Batch(m.closeExecView(), m.dialogs.FlashError("Run failed: "+msg.Err.Error(), 6*time.Second))
		}
		return m, m.dialogs.FlashError("Run failed: "+msg.Err.Error(), 6*time.Second)
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
		return m, m.dialogs.FlashError("Delete failed: "+msg.Err.Error(), 6*time.Second)
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
	cmds = append(cmds, m.dialogs.FlashUndo("Deleted run", undo, 6*time.Second))
	return m, tea.Batch(cmds...)
}

// handleBulkAction applies the result of a bulk run operation (or its undo):
// refreshes the list and flashes a count. Bulk delete is itself undoable.
func (m Model) handleBulkAction(msg uikit.BulkActionMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		return m, m.dialogs.FlashError(msg.Action+" failed: "+msg.Err.Error(), 6*time.Second)
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
		return m, m.dialogs.FlashError("Delete failed: "+msg.Err.Error(), 6*time.Second)
	}
	cmds := []tea.Cmd{m.fetchExecWindow()}
	label := fmt.Sprintf("Deleted %d run%s", msg.Affected, textutil.Pluralize(msg.Affected, "", "s"))
	if !msg.Restore.MatchAll && msg.Affected > 0 {
		undo := m.streams.RestoreRuns(msg.Restore)
		cmds = append(cmds, m.dialogs.FlashUndo(label, undo, 6*time.Second))
	} else {
		cmds = append(cmds, m.dialogs.Flash(label, 4*time.Second))
	}
	return m, tea.Batch(cmds...)
}

// handleSchedulePause applies a pause or resume (or its undo): the header flips
// at once, a fetch picks up the daemon's pausedAt, and the toast offers undo.
func (m Model) handleSchedulePause(msg uikit.SchedulePauseMsg) (tea.Model, tea.Cmd) {
	verb, failed := "Resumed", "Resume failed: "
	if msg.Paused {
		verb, failed = "Paused", "Pause failed: "
	}
	m.logActionResult(verb+" schedule of", msg.TaskName, msg.Err)
	if msg.Err != nil {
		reason := msg.Err.Error()
		var statusErr *apiclient.HTTPStatusError
		if errors.As(msg.Err, &statusErr) {
			reason = statusErr.Detail()
		}
		return m, m.dialogs.Flash(failed+reason, 6*time.Second)
	}
	paused := maps.Clone(m.info.PausedTasks)
	if paused == nil {
		paused = make(map[string]time.Time)
	}
	if msg.Paused {
		paused[msg.TaskName] = time.Now()
	} else {
		delete(paused, msg.TaskName)
	}
	m.info.PausedTasks = paused
	undo := m.streams.SetSchedulePaused(msg.TaskName, !msg.Paused)
	label := fmt.Sprintf("%s the schedule of '%s' · press u to undo", verb, msg.TaskName)
	return m, tea.Batch(m.streams.FetchTaskState(), m.dialogs.FlashUndo(label, undo, 6*time.Second))
}

func (m Model) handleRestartService(msg uikit.RestartServiceMsg) (tea.Model, tea.Cmd) {
	m.logActionResult("Restarted service", msg.TaskName, msg.Err)
	if msg.Err != nil {
		return m, m.dialogs.FlashError("Restart failed: "+msg.Err.Error(), 6*time.Second)
	}
	m.setServiceStopped(msg.TaskName, false)
	if m.execView == nil || m.execView.Run == nil || m.execView.Run.TaskName != msg.TaskName {
		return m, nil
	}
	// The open run is a pre-restart instance, now dead. Leave it for the fresh
	// instance when it's already running, else for the parent screen (the SSE
	// auto-open follows a single-instance service there once it starts).
	oldID := m.execView.Run.ID
	cmds := []tea.Cmd{m.closeExecView()}
	if run := m.execWindow.LatestRunning(msg.TaskName); run != nil && run.ID != oldID && m.isSingleInstanceService(msg.TaskName) {
		cmds = append(cmds, m.openExecView(run))
	}
	return m, tea.Batch(cmds...)
}

func (m Model) handleStopService(msg uikit.StopServiceMsg) (tea.Model, tea.Cmd) {
	m.logActionResult("Stopped service", msg.TaskName, msg.Err)
	if msg.Err != nil {
		return m, m.dialogs.FlashError("Stop failed: "+msg.Err.Error(), 6*time.Second)
	}
	m.setServiceStopped(msg.TaskName, true)
	if m.execView != nil && m.execView.Run != nil && m.execView.Run.TaskName == msg.TaskName {
		m.execView.SetServiceStopped(true)
	}
	return m, nil
}

// setServiceStopped records an operator stop/start ahead of the next
// /api/tasks poll, so a run opened in between shows the right action.
func (m *Model) setServiceStopped(taskName string, stopped bool) {
	if !stopped {
		delete(m.info.StoppedServices, taskName)
		return
	}
	if m.info.StoppedServices == nil {
		m.info.StoppedServices = make(map[string]bool)
	}
	m.info.StoppedServices[taskName] = true
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
		return m, m.dialogs.FlashError("Failed to load frame history", 3*time.Second)
	}
	if len(msg.Frames) == 0 {
		return m, m.dialogs.Flash("No frame history for this line", 3*time.Second)
	}
	m.dialogs.Show(dlgLogHistory, NewLogHistoryDialog(msg.Line, msg.Frames, msg.Committed))
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
		cmds = append(cmds, m.streams.FetchDaemonInfo(), m.streams.FetchTaskState())
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
// /api/daemon poll — currently the config-stale notice and the service-managed
// flag (the daemon may be restarted under a service manager mid-session).
// Errors are ignored: the header just keeps its last known state.
func (m Model) handleDaemonInfo(msg uikit.DaemonInfoMsg) (tea.Model, tea.Cmd) {
	if msg.Err == nil && msg.Info != nil {
		m.info.ConfigStale = msg.Info.ConfigStale
		m.info.ConfigWarnings = msg.Info.ConfigWarnings
		m.info.ServiceManaged = msg.Info.ServiceManaged
		if msg.Info.ResolvedTimezone != m.info.Timezone {
			m.info.Timezone = msg.Info.ResolvedTimezone
			m.info.TimezoneSource = msg.Info.TimezoneSource
			m.loc = uikit.ResolveLocation(m.info.Timezone)
			m.execWindow.SetLocation(m.loc)
		}
		m.sidebar.SetUpdate(msg.Info.UpdateAvailable, msg.Info.LatestVersion)
	}
	return m, nil
}

// reloadConfig triggers an explicit config reload from inside the TUI. The
// result arrives as a ReloadResultMsg.
func (m *Model) reloadConfig() tea.Cmd {
	return tea.Batch(m.dialogs.Flash("Reloading config…", 3*time.Second), m.streams.Reload())
}

// handleReloadResult applies an operator-triggered reload: on success it adopts
// the fresh task set, rebuilds the sidebar (preserving the active task), clears
// the config-stale notice, and flashes a summary; on failure it surfaces the
// daemon's reason and leaves the running set untouched.
func (m Model) handleReloadResult(msg uikit.ReloadResultMsg) (tea.Model, tea.Cmd) {
	if msg.Err != nil {
		return m, m.dialogs.FlashError("Reload failed: "+msg.Err.Error(), 6*time.Second)
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
		// A reload clears the pause of a task it made unpausable.
		cmds = append(cmds, m.fetchExecWindow(), m.streams.FetchTaskState())
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
	if len(r.Settings) > 0 {
		parts = append(parts, "settings updated")
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
		m.execView.Run.ID,
		int64(m.execView.Pane.FirstLoadedLineNum()),
		int64(execlist.LogTailLines),
	)
}

// handleSSEEvent processes a parsed event off the unified stream: run
// lifecycle events (the default, handled below) and notification events
// (created/updated/unreadCountChanged) share this one connection.
func (m *Model) handleSSEEvent(evt apiclient.RunStreamEvent) tea.Cmd {
	if cmd, handled := m.handleNotificationSSEEvent(evt); handled {
		return cmd
	}
	if evt.Type == string(events.EventSystemSample) {
		m.handleSystemSample(evt)
		return nil
	}

	var runEvt server.RunEventBody
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
			m.sidebar.ActiveTask() == runEvt.Run.TaskName &&
			m.isSingleInstanceService(runEvt.Run.TaskName) {
			return m.openExecView(runEvt.Run)
		}
	}

	return nil
}

// handleSystemSample takes the live CPU and memory use off a system sample, so
// the task header, the Info page and an open run's header follow it as it
// changes.
func (m *Model) handleSystemSample(evt apiclient.RunStreamEvent) {
	var sample server.SystemSampleSSEEvent
	if err := json.Unmarshal(evt.Data, &sample); err != nil {
		m.debugView.AppendLine("Failed to parse system sample: " + err.Error())
		return
	}
	m.info.TaskUsage = sample.Tasks
	m.info.RunUsage = sample.Runs
	m.infoView.SetTaskUsage(sample.Tasks)
	if m.execView != nil {
		m.execView.Usage = m.runUsage(m.execView.RunID())
	}
}

// handleNotificationSSEEvent handles the notification.* event types that
// ride the unified stream alongside run lifecycle events. handled is false
// for any other event type, telling the caller to fall through to the run
// lifecycle path.
func (m *Model) handleNotificationSSEEvent(evt apiclient.RunStreamEvent) (cmd tea.Cmd, handled bool) {
	switch evt.Type {
	case "notification.created", "notification.updated":
		env, err := apiclient.DecodeNotificationEnvelope(evt.Data)
		if err != nil {
			m.debugView.AppendLine("Failed to parse notification: " + err.Error())
			return nil, true
		}
		m.notifications.SetUnread(int(env.UnreadCount))
		if m.notifications.Upsert(env.Notification) {
			m.updateLayout()
		}
		return nil, true
	case "notification.unreadCountChanged":
		count, err := apiclient.DecodeUnreadCountEnvelope(evt.Data)
		if err != nil {
			m.debugView.AppendLine("Failed to parse unread count: " + err.Error())
			return nil, true
		}
		m.notifications.SetUnread(int(count))
		m.updateLayout()
		return nil, true
	default:
		return nil, false
	}
}

const logReconnectDelay = 500 * time.Millisecond

// startShutdownSpinner transitions the confirm dialog into the shutting-down
// spinner state and fires the shutdown function as a background command.
// If no dialog is open (e.g. uikit.QuitMsg arrived without a confirm), a new one is
// created so the spinner has somewhere to render.
func (m *Model) startShutdownSpinner() tea.Cmd {
	if !m.dialogs.Has(dlgConfirm) {
		dialog := NewConfirmDialog("Quit", "", nil)
		m.dialogs.Show(dlgConfirm, dialog)
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
