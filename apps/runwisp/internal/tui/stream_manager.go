// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime/retry"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/execlist"
	"github.com/runwisp/runwisp/internal/tui/views/notifications"
)

// StreamManager owns SSE event subscriptions, log streaming, and data fetching.
// Extracted from Model to isolate I/O and async concerns.
//
// The base context is owned by this manager because each subscription derives
// a child context from it; shutting the manager down cancels every in-flight
// stream in one step. Passing context as a method parameter would require the
// caller to thread the same lifecycle context through every SSE subscribe call
// — that responsibility belongs here.
type StreamManager struct {
	streamCtx context.Context //NOSONAR: lifecycle ctx owned by the manager; cancelled via Shutdown()
	cancel    context.CancelFunc
	client    *apiclient.Client

	logCancel context.CancelFunc
	logCh     <-chan apiclient.LogStreamMsg
	sseCh     <-chan apiclient.RunStreamEvent

	daemonLogCh    <-chan string
	notificationCh <-chan apiclient.NotificationStreamEvent
}

func NewStreamManager(client *apiclient.Client) StreamManager {
	ctx, cancel := context.WithCancel(context.Background())
	return StreamManager{
		streamCtx: ctx,
		cancel:    cancel,
		client:    client,
	}
}

// Shutdown cancels all active streams.
func (sm *StreamManager) Shutdown() {
	sm.cancel()
}

// SubscribeEvents connects to the SSE run-events stream.
func (sm *StreamManager) SubscribeEvents() tea.Cmd {
	return func() tea.Msg {
		if sm.client == nil {
			return nil
		}
		ch, err := sm.client.StreamRunEvents(sm.streamCtx)
		if err != nil {
			return uikit.DebugLogMsg{Message: "Events stream failed: " + err.Error()}
		}
		return uikit.SSEConnectedMsg{Ch: ch}
	}
}

// OnSSEConnected stores the channel and returns a command to start listening.
func (sm *StreamManager) OnSSEConnected(ch <-chan apiclient.RunStreamEvent) tea.Cmd {
	sm.sseCh = ch
	return listenSSE(ch)
}

// ContinueListeningSSE returns a command to listen for the next SSE event.
func (sm *StreamManager) ContinueListeningSSE() tea.Cmd {
	if sm.sseCh != nil {
		return listenSSE(sm.sseCh)
	}
	return nil
}

// StartLogStream opens a single line-based SSE stream for the run. fromLine
// is an absolute line anchor; pass a negative value (e.g. -execlist.LogTailLines) to
// land at the end of the log immediately. Cancels any previous stream owned
// by this manager.
func (sm *StreamManager) StartLogStream(run *model.Run, fromLine int64) tea.Cmd {
	if sm.client == nil {
		return nil
	}

	if sm.logCancel != nil {
		sm.logCancel()
	}
	ctx, cancel := context.WithCancel(sm.streamCtx)
	sm.logCancel = cancel

	runID := run.ID
	taskName := run.TaskName
	client := sm.client

	return func() tea.Msg {
		ch, err := client.StreamLogLines(ctx, taskName, runID, apiclient.StreamLogOpts{FromLine: fromLine})
		if err != nil {
			return uikit.LogDoneMsg{RunID: runID}
		}
		return uikit.LogStreamConnectedMsg{RunID: runID, Ch: ch}
	}
}

// FetchLogTail loads the last `tail` lines of a run's log in one REST page so
// the viewer can paint at the bottom immediately, without replaying the whole
// tail window line-by-line over SSE. The caller then opens the live stream for
// just the lines after this page.
func (sm *StreamManager) FetchLogTail(run *model.Run, tail int64) tea.Cmd {
	if sm.client == nil {
		return nil
	}
	client := sm.client
	taskName := run.TaskName
	runID := run.ID
	return func() tea.Msg {
		page, err := client.GetLogPage(taskName, runID, -tail, tail)
		if err != nil {
			// Degrade gracefully: an empty tail makes the handler open the live
			// stream at the tail anchor, i.e. the old line-by-line backfill.
			return uikit.LogTailLoadedMsg{RunID: runID}
		}
		return uikit.LogTailLoadedMsg{
			RunID:     runID,
			Lines:     page.Lines,
			Total:     page.TotalLines,
			Finalized: page.Finalized,
		}
	}
}

// FetchOlderLogs fetches lines before the currently loaded range for scroll-up
// via the JSON page endpoint. beforeLine is the absolute line number of the
// first currently-loaded entry; the returned page covers
// [max(0, beforeLine-count), beforeLine).
func (sm *StreamManager) FetchOlderLogs(taskName, runID string, beforeLine, count int64) tea.Cmd {
	if sm.client == nil {
		return nil
	}

	client := sm.client
	startLine := max(beforeLine-count, 0)
	limit := beforeLine - startLine
	if limit <= 0 {
		return nil
	}

	return func() tea.Msg {
		page, err := client.GetLogPage(taskName, runID, startLine, limit)
		if err != nil {
			return uikit.DebugLogMsg{Message: "Failed to load older logs: " + err.Error()}
		}
		first := startLine
		if len(page.Lines) > 0 {
			first = page.Lines[0].N
		}
		return uikit.LogOlderLoadedMsg{
			RunID:     runID,
			Lines:     page.Lines,
			FirstLine: first,
			Total:     page.TotalLines,
		}
	}
}

// FetchLineHistory loads the prior whole-region frames an anchor line animated
// through. committed is the line's final on-disk text, passed through so the
// viewer can label where the animation settled.
func (sm *StreamManager) FetchLineHistory(taskName, runID string, lineNum int64, committed string) tea.Cmd {
	if sm.client == nil {
		return nil
	}
	client := sm.client
	return func() tea.Msg {
		frames, err := client.GetLogLineHistory(taskName, runID, lineNum)
		return uikit.LogLineHistoryMsg{
			RunID:     runID,
			Line:      lineNum,
			Frames:    frames,
			Committed: committed,
			Err:       err,
		}
	}
}

// OnLogConnected stores the log channel and returns a command to start listening.
func (sm *StreamManager) OnLogConnected(runID string, ch <-chan apiclient.LogStreamMsg) tea.Cmd {
	sm.logCh = ch
	return listenLogStream(runID, ch)
}

// ContinueListeningLog returns a command to listen for the next log chunk.
func (sm *StreamManager) ContinueListeningLog(runID string) tea.Cmd {
	if sm.logCh != nil {
		return listenLogStream(runID, sm.logCh)
	}
	return nil
}

// CancelLogStream stops the current log stream.
func (sm *StreamManager) CancelLogStream() {
	if sm.logCancel != nil {
		sm.logCancel()
		sm.logCancel = nil
	}
	sm.logCh = nil
}

// FetchExecWindow returns a command that loads execution data for the viewport.
func (sm *StreamManager) FetchExecWindow(window *execlist.ExecWindow, scroll, vpH int) tea.Cmd {
	fn := window.FetchAroundCmd(scroll, vpH)
	if fn == nil {
		return nil
	}
	return func() tea.Msg {
		items, offset, total, err := fn()
		if err != nil {
			return uikit.DebugLogMsg{Message: "Failed to load runs: " + err.Error()}
		}
		return uikit.ExecWindowFetchedMsg{Items: items, Offset: offset, Total: total}
	}
}

// FetchSystemStats returns a command that polls live system metrics.
func (sm *StreamManager) FetchSystemStats() tea.Cmd {
	if sm.client == nil {
		return nil
	}
	client := sm.client
	return func() tea.Msg {
		stats, err := client.GetSystemStats()
		return uikit.SystemStatsMsg{Stats: stats, Err: err}
	}
}

// FetchDaemonInfo returns a command that re-reads /api/info. The daemon
// recomputes config_stale per request, so polling this keeps the
// "restart to apply" notice live.
func (sm *StreamManager) FetchDaemonInfo() tea.Cmd {
	if sm.client == nil {
		return nil
	}
	client := sm.client
	return func() tea.Msg {
		info, err := client.GetDaemonInfo()
		return uikit.DaemonInfoMsg{Info: info, Err: err}
	}
}

// Reload triggers an explicit config reload (POST /api/reload). On success it
// follows up with a fresh /api/info read so the caller can rebuild the sidebar
// and clear the config-stale notice in one step. A reload error (e.g. a
// restart-only setting changed) is reported as-is — the running task set is
// untouched by the daemon.
func (sm *StreamManager) Reload() tea.Cmd {
	if sm.client == nil {
		return nil
	}
	client := sm.client
	return func() tea.Msg {
		result, err := client.Reload()
		if err != nil {
			return uikit.ReloadResultMsg{Err: err}
		}
		// The reload applied; refresh the task list. If this read fails the
		// reload still stands — report it without fresh tasks.
		info, infoErr := client.GetDaemonInfo()
		if infoErr != nil {
			return uikit.ReloadResultMsg{Result: result}
		}
		return uikit.ReloadResultMsg{Result: result, Info: info}
	}
}

// RestoreRuns un-deletes every run matched by sel (the inverse of a delete).
func (sm *StreamManager) RestoreRuns(sel model.RunSelector) tea.Cmd {
	return sm.bulkAction("Restored", func(c *apiclient.Client) (int, error) {
		return c.BulkRestoreRuns(sel)
	})
}

// CancelRuns cancels every running/queued run matched by sel.
func (sm *StreamManager) CancelRuns(sel model.RunSelector) tea.Cmd {
	return sm.bulkAction("Cancelled", func(c *apiclient.Client) (int, error) {
		return c.BulkCancelRuns(sel)
	})
}

// RerunRuns triggers a fresh run for every run matched by sel.
func (sm *StreamManager) RerunRuns(sel model.RunSelector) tea.Cmd {
	return sm.bulkAction("Reran", func(c *apiclient.Client) (int, error) {
		refs, err := c.BulkRerunRuns(sel)
		return len(refs), err
	})
}

// DeleteRunsUndoable soft-deletes every run matched by sel and reports the
// selector back so the caller can offer an undo (restore). Distinct from
// DeleteRuns, whose generic BulkActionMsg only flashes a count and would clobber
// an undo toast.
func (sm *StreamManager) DeleteRunsUndoable(sel model.RunSelector) tea.Cmd {
	if sm.client == nil {
		return nil
	}
	client := sm.client
	return func() tea.Msg {
		n, err := client.BulkDeleteRuns(sel)
		return uikit.BulkDeleteResultMsg{Affected: n, Restore: sel, Err: err}
	}
}

// bulkAction wraps a bulk client call as a tea.Cmd yielding a BulkActionMsg.
func (sm *StreamManager) bulkAction(verb string, fn func(*apiclient.Client) (int, error)) tea.Cmd {
	if sm.client == nil {
		return nil
	}
	client := sm.client
	return func() tea.Msg {
		n, err := fn(client)
		return uikit.BulkActionMsg{Action: verb, Affected: n, Err: err}
	}
}

// FetchRunSummary returns a command that fetches aggregate run statistics.
func (sm *StreamManager) FetchRunSummary() tea.Cmd {
	if sm.client == nil {
		return nil
	}
	client := sm.client
	return func() tea.Msg {
		summary, err := client.GetRunSummary()
		return uikit.RunSummaryMsg{Summary: summary, Err: err}
	}
}

// taskSummaryWindow is how many recent runs FetchTaskSummary classifies for the
// on-demand task-detail panel. The all-time total comes from the same response's
// row count; the success/failed breakdown is over this recent window so the
// panel reflects current health rather than an all-time tally skewed by ancient
// failures.
const taskSummaryWindow = 100

// FetchTaskSummary returns a command that loads the per-task health figures for
// the task-detail panel: the all-time run count plus a success/failed/other
// breakdown over the most recent runs. It reuses the existing per-task runs
// endpoint rather than a dedicated aggregate, so no new server surface is
// needed.
func (sm *StreamManager) FetchTaskSummary(taskName string) tea.Cmd {
	if sm.client == nil || taskName == "" {
		return nil
	}
	client := sm.client
	return func() tea.Msg {
		runs, total, err := client.ListRunsByTask(taskName, apiclient.RunsParams{
			Limit:         taskSummaryWindow,
			SortField:     "createdAt",
			SortDirection: "desc",
		})
		if err != nil {
			return uikit.TaskSummaryMsg{TaskName: taskName, Err: err}
		}
		return summarizeTaskRuns(taskName, runs, total)
	}
}

// summarizeTaskRuns classifies a newest-first page of runs into the
// success/failed/other breakdown the task-detail panel shows. It is pure so the
// classification (which failure reasons count, where last-failure comes from)
// is testable without a client. Runs must be ordered newest-first so the first
// failure encountered is the most recent.
func summarizeTaskRuns(taskName string, runs []model.Run, total int64) uikit.TaskSummaryMsg {
	msg := uikit.TaskSummaryMsg{TaskName: taskName, Total: total, Window: len(runs)}
	for i := range runs {
		run := &runs[i]
		switch {
		case run.EndReason != nil && *run.EndReason == model.ReasonSuccess:
			msg.Success++
		case run.EndReason != nil && retry.IsFailureReason(*run.EndReason):
			msg.Failed++
			if msg.LastFailure == nil {
				msg.LastFailure = run.EndAt
			}
		default:
			msg.Other++
		}
	}
	return msg
}

// FetchMetricsHistory returns a command that fetches historical system metrics.
func (sm *StreamManager) FetchMetricsHistory() tea.Cmd {
	if sm.client == nil {
		return nil
	}
	client := sm.client
	return func() tea.Msg {
		samples, err := client.GetMetricsHistory()
		return uikit.MetricsHistoryMsg{Samples: samples, Err: err}
	}
}

// listenSSE waits for the next event from the SSE channel.
func listenSSE(ch <-chan apiclient.RunStreamEvent) tea.Cmd {
	return listenChannel(ch, func(event apiclient.RunStreamEvent) tea.Msg {
		return uikit.SSEEventMsg{Event: event}
	}, uikit.SSEDisconnectedMsg{})
}

// listenLogStream waits for the next message from the line-based SSE channel.
// Each delivered LogStreamMsg is mapped onto its concrete TUI message type.
func listenLogStream(runID string, ch <-chan apiclient.LogStreamMsg) tea.Cmd {
	return listenChannel(ch, func(msg apiclient.LogStreamMsg) tea.Msg {
		switch msg.Kind {
		case apiclient.LogStreamMsgKindLine:
			return uikit.LogLineMsg{RunID: runID, Line: msg.Line}
		case apiclient.LogStreamMsgKindRegion:
			return uikit.LogRegionMsg{RunID: runID, Stream: msg.Region.Stream, Epoch: msg.Region.Epoch, Rows: msg.Region.Rows}
		case apiclient.LogStreamMsgKindRotated:
			return uikit.LogRotatedMsg{RunID: runID, FirstAvailable: msg.Rotated.FirstAvailable}
		case apiclient.LogStreamMsgKindDropped:
			return uikit.LogDroppedMsg{RunID: runID, After: msg.Dropped.After, Count: msg.Dropped.Count}
		case apiclient.LogStreamMsgKindDone:
			return uikit.LogDoneMsg{RunID: runID, FinalLine: msg.Done.FinalLine}
		case apiclient.LogStreamMsgKindErr:
			return uikit.LogDoneMsg{RunID: runID}
		}
		return nil
	}, uikit.LogDoneMsg{RunID: runID})
}

// SubscribeDaemonLogs connects to the daemon log SSE stream.
func (sm *StreamManager) SubscribeDaemonLogs() tea.Cmd {
	if sm.client == nil {
		return nil
	}
	client := sm.client
	ctx := sm.streamCtx
	return func() tea.Msg {
		ch, err := client.StreamDaemonLogs(ctx)
		if err != nil {
			return uikit.DebugLogMsg{Message: "Daemon log stream failed: " + err.Error()}
		}
		return uikit.DaemonLogConnectedMsg{Ch: ch}
	}
}

// OnDaemonLogConnected stores the channel and returns a command to start listening.
func (sm *StreamManager) OnDaemonLogConnected(ch <-chan string) tea.Cmd {
	sm.daemonLogCh = ch
	return listenDaemonLog(ch)
}

// ContinueListeningDaemonLog returns a command to listen for the next daemon log line.
func (sm *StreamManager) ContinueListeningDaemonLog() tea.Cmd {
	if sm.daemonLogCh != nil {
		return listenDaemonLog(sm.daemonLogCh)
	}
	return nil
}

// SubscribeNotifications connects to the SSE notifications stream.
func (sm *StreamManager) SubscribeNotifications() tea.Cmd {
	if sm.client == nil {
		return nil
	}
	client := sm.client
	ctx := sm.streamCtx
	return func() tea.Msg {
		ch, err := client.StreamNotifications(ctx)
		if err != nil {
			return uikit.DebugLogMsg{Message: "Notifications stream failed: " + err.Error()}
		}
		return uikit.NotificationStreamConnectedMsg{Ch: ch}
	}
}

// OnNotificationConnected stores the channel and returns a command to listen.
func (sm *StreamManager) OnNotificationConnected(ch <-chan apiclient.NotificationStreamEvent) tea.Cmd {
	sm.notificationCh = ch
	return listenNotifications(ch)
}

// ContinueListeningNotifications returns a command to wait for the next event.
func (sm *StreamManager) ContinueListeningNotifications() tea.Cmd {
	if sm.notificationCh != nil {
		return listenNotifications(sm.notificationCh)
	}
	return nil
}

// FetchUnreadCount returns a command that loads the snapshot unread count.
func (sm *StreamManager) FetchUnreadCount() tea.Cmd {
	if sm.client == nil {
		return nil
	}
	client := sm.client
	return func() tea.Msg {
		count, err := client.UnreadNotificationCount()
		return uikit.NotificationUnreadCountMsg{Count: count, Err: err}
	}
}

// FetchNotifications returns a command that loads the most recent page of
// notifications. The result seeds the panel so the expanded view isn't empty
// while the SSE stream waits for new events.
func (sm *StreamManager) FetchNotifications() tea.Cmd {
	if sm.client == nil {
		return nil
	}
	client := sm.client
	return func() tea.Msg {
		page, err := client.ListNotifications(notifications.InitialPageSize, "")
		if err != nil {
			return uikit.NotificationsLoadedMsg{Err: err}
		}
		return uikit.NotificationsLoadedMsg{Items: page.Items}
	}
}

// MarkNotificationRead persists a single notification's read state.
func (sm *StreamManager) MarkNotificationRead(id string) tea.Cmd {
	if sm.client == nil || id == "" {
		return nil
	}
	client := sm.client
	return func() tea.Msg {
		err := client.MarkNotificationRead(id)
		return uikit.NotificationReadStateMsg{ID: id, Read: true, Err: err}
	}
}

// MarkAllNotificationsRead persists a "mark every unread row read" action. The
// panel clears its badge optimistically; on failure the next SSE unread-count
// event re-syncs, so this only surfaces an error to the debug log.
func (sm *StreamManager) MarkAllNotificationsRead() tea.Cmd {
	if sm.client == nil {
		return nil
	}
	client := sm.client
	return func() tea.Msg {
		if err := client.MarkAllNotificationsRead(); err != nil {
			return uikit.DebugLogMsg{Message: "Failed to mark all notifications read: " + err.Error()}
		}
		return nil
	}
}

// MarkNotificationUnread clears a single notification's read state.
func (sm *StreamManager) MarkNotificationUnread(id string) tea.Cmd {
	if sm.client == nil || id == "" {
		return nil
	}
	client := sm.client
	return func() tea.Msg {
		err := client.MarkNotificationUnread(id)
		return uikit.NotificationReadStateMsg{ID: id, Read: false, Err: err}
	}
}

func listenNotifications(ch <-chan apiclient.NotificationStreamEvent) tea.Cmd {
	return listenChannel(ch, func(event apiclient.NotificationStreamEvent) tea.Msg {
		return uikit.NotificationEventMsg{Event: event}
	}, uikit.NotificationStreamDisconnectedMsg{})
}

func listenDaemonLog(ch <-chan string) tea.Cmd {
	return listenChannel(ch, func(line string) tea.Msg {
		return uikit.DaemonLogLineMsg{Line: line}
	}, uikit.DaemonLogDisconnectedMsg{})
}

func listenChannel[T any](ch <-chan T, onValue func(T) tea.Msg, onClosed tea.Msg) tea.Cmd {
	return func() tea.Msg {
		value, ok := <-ch
		if !ok {
			return onClosed
		}
		return onValue(value)
	}
}
