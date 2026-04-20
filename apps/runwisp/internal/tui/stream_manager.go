// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
)

// StreamManager owns SSE event subscriptions, log streaming, and data fetching.
// Extracted from Model to isolate I/O and async concerns.
type StreamManager struct {
	ctx    context.Context
	cancel context.CancelFunc
	client *apiclient.Client

	logCancel context.CancelFunc
	logCh     <-chan string
	sseCh     <-chan apiclient.RunStreamEvent

	daemonLogCh <-chan string
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

// StartLogStream begins streaming logs for a run via the API.
// Cancels any previous log stream.
func (sm *StreamManager) StartLogStream(run *model.Run) tea.Cmd {
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
		ch, err := client.StreamLog(ctx, taskName, runID)
		if err != nil {
			content, fetchErr := client.GetLog(taskName, runID)
			if fetchErr != nil {
				return logDoneMsg{RunID: runID}
			}
			lines := strings.Split(content, "\n")
			return logBatchMsg{RunID: runID, Lines: lines}
		}
		return logStreamConnectedMsg{RunID: runID, Ch: ch}
	}
}

// OnLogConnected stores the log channel and returns a command to start listening.
func (sm *StreamManager) OnLogConnected(runID string, ch <-chan string) tea.Cmd {
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

// listenLogStream waits for the next log chunk from the channel.
func listenLogStream(runID string, ch <-chan string) tea.Cmd {
	return listenChannel(ch, func(chunk string) tea.Msg {
		return logChunkMsg{RunID: runID, Chunk: chunk}
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
