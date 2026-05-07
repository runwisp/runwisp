// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
)

const logTailLines = 1000

// StreamManager owns SSE event subscriptions, log streaming, and data fetching.
// Extracted from Model to isolate I/O and async concerns.
type StreamManager struct {
	ctx    context.Context
	cancel context.CancelFunc
	client *apiclient.Client

	logCancel context.CancelFunc
	logCh     <-chan apiclient.LogStreamMsg
	sseCh     <-chan apiclient.RunStreamEvent

	daemonLogCh    <-chan string
	notificationCh <-chan apiclient.NotificationStreamEvent
}

func NewStreamManager(client *apiclient.Client) StreamManager {
	ctx, cancel := context.WithCancel(context.Background())
	return StreamManager{
		ctx:    ctx,
		cancel: cancel,
		client: client,
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
		ch, err := sm.client.StreamRunEvents(sm.ctx)
		if err != nil {
			return DebugLogMsg{Message: "Events stream failed: " + err.Error()}
		}
		return sseConnectedMsg{ch: ch}
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
// is an absolute line anchor; pass a negative value (e.g. -logTailLines) to
// land at the end of the log immediately. Cancels any previous stream owned
// by this manager.
func (sm *StreamManager) StartLogStream(run *model.Run, fromLine int64) tea.Cmd {
	if sm.client == nil {
		return nil
	}

	if sm.logCancel != nil {
		sm.logCancel()
	}
	ctx, cancel := context.WithCancel(sm.ctx)
	sm.logCancel = cancel

	runID := run.ID
	taskName := run.TaskName
	client := sm.client

	return func() tea.Msg {
		ch, err := client.StreamLogLines(ctx, taskName, runID, apiclient.StreamLogOpts{FromLine: fromLine})
		if err != nil {
			return logDoneMsg{RunID: runID}
		}
		return logStreamConnectedMsg{RunID: runID, Ch: ch}
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
	startLine := beforeLine - count
	if startLine < 0 {
		startLine = 0
	}
	limit := beforeLine - startLine
	if limit <= 0 {
		return nil
	}

	return func() tea.Msg {
		page, err := client.GetLogPage(taskName, runID, startLine, limit)
		if err != nil {
			return DebugLogMsg{Message: "Failed to load older logs: " + err.Error()}
		}
		first := startLine
		if len(page.Lines) > 0 {
			first = page.Lines[0].N
		}
		return logOlderLoadedMsg{
			RunID:     runID,
			Lines:     page.Lines,
			FirstLine: first,
			Total:     page.TotalLines,
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
func (sm *StreamManager) FetchExecWindow(window *ExecWindow, scroll, vpH int) tea.Cmd {
	fn := window.FetchAroundCmd(scroll, vpH)
	if fn == nil {
		return nil
	}
	return func() tea.Msg {
		items, offset, total, err := fn()
		if err != nil {
			return DebugLogMsg{Message: "Failed to load runs: " + err.Error()}
		}
		return execWindowFetchedMsg{Items: items, Offset: offset, Total: total}
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
		return systemStatsMsg{Stats: stats, Err: err}
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
		return runSummaryMsg{Summary: summary, Err: err}
	}
}

// FetchMetricsHistory returns a command that fetches historical system metrics.
func (sm *StreamManager) FetchMetricsHistory() tea.Cmd {
	if sm.client == nil {
		return nil
	}
	client := sm.client
	return func() tea.Msg {
		samples, err := client.GetMetricsHistory()
		return metricsHistoryMsg{Samples: samples, Err: err}
	}
}

// listenSSE waits for the next event from the SSE channel.
func listenSSE(ch <-chan apiclient.RunStreamEvent) tea.Cmd {
	return listenChannel(ch, func(event apiclient.RunStreamEvent) tea.Msg {
		return sseEventMsg{Event: event}
	}, sseDisconnectedMsg{})
}

// listenLogStream waits for the next message from the line-based SSE channel.
// Each delivered LogStreamMsg is mapped onto its concrete TUI message type.
func listenLogStream(runID string, ch <-chan apiclient.LogStreamMsg) tea.Cmd {
	return listenChannel(ch, func(msg apiclient.LogStreamMsg) tea.Msg {
		switch msg.Kind {
		case apiclient.LogStreamMsgKindLine:
			return logLineMsg{RunID: runID, Line: msg.Line}
		case apiclient.LogStreamMsgKindRotated:
			return logRotatedMsg{RunID: runID, FirstAvailable: msg.Rotated.FirstAvailable}
		case apiclient.LogStreamMsgKindDropped:
			return logDroppedMsg{RunID: runID, After: msg.Dropped.After, Count: msg.Dropped.Count}
		case apiclient.LogStreamMsgKindDone:
			return logDoneMsg{RunID: runID, FinalLine: msg.Done.FinalLine}
		case apiclient.LogStreamMsgKindErr:
			return logDoneMsg{RunID: runID}
		}
		return nil
	}, logDoneMsg{RunID: runID})
}

// SubscribeDaemonLogs connects to the daemon log SSE stream.
func (sm *StreamManager) SubscribeDaemonLogs() tea.Cmd {
	if sm.client == nil {
		return nil
	}
	client := sm.client
	ctx := sm.ctx
	return func() tea.Msg {
		ch, err := client.StreamDaemonLogs(ctx)
		if err != nil {
			return DebugLogMsg{Message: "Daemon log stream failed: " + err.Error()}
		}
		return daemonLogConnectedMsg{ch: ch}
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
	ctx := sm.ctx
	return func() tea.Msg {
		ch, err := client.StreamNotifications(ctx)
		if err != nil {
			return DebugLogMsg{Message: "Notifications stream failed: " + err.Error()}
		}
		return notificationStreamConnectedMsg{ch: ch}
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
		return notificationUnreadCountMsg{Count: count, Err: err}
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
		page, err := client.ListNotifications(notificationsInitialPageSize, "")
		if err != nil {
			return notificationsLoadedMsg{Err: err}
		}
		return notificationsLoadedMsg{Items: page.Items}
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
		return notificationReadStateMsg{ID: id, Read: true, Err: err}
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
		return notificationReadStateMsg{ID: id, Read: false, Err: err}
	}
}

func listenNotifications(ch <-chan apiclient.NotificationStreamEvent) tea.Cmd {
	return listenChannel(ch, func(event apiclient.NotificationStreamEvent) tea.Msg {
		return notificationEventMsg{Event: event}
	}, notificationStreamDisconnectedMsg{})
}

func listenDaemonLog(ch <-chan string) tea.Cmd {
	return listenChannel(ch, func(line string) tea.Msg {
		return daemonLogLineMsg{Line: line}
	}, daemonLogDisconnectedMsg{})
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
