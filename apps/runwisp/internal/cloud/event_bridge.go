// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"context"

	"log/slog"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
)

// EventBridge subscribes to runtime events and forwards execution status
// and log lines to the cloud connection.
type EventBridge struct {
	eventBus      EventSubscriber
	handler       *InboundHandler
	tracker       *ExecutionTracker
	sendReady     func(any) error
	onStateChange func()
	unsubscribers []func()
}

func NewEventBridge(
	eventBus EventSubscriber,
	handler *InboundHandler,
	tracker *ExecutionTracker,
	sendReady func(any) error,
	onStateChange func(),
) *EventBridge {
	return &EventBridge{
		eventBus:      eventBus,
		handler:       handler,
		tracker:       tracker,
		sendReady:     sendReady,
		onStateChange: onStateChange,
	}
}

// Start registers event listeners on the event bus. ctx is threaded into log
// archival so in-flight uploads abort when the daemon shuts down.
func (b *EventBridge) Start(ctx context.Context) {
	b.unsubscribers = append(b.unsubscribers,
		b.eventBus.Subscribe(events.EventRunStarted, func(e events.Event) { b.handleRunEvent(ctx, e) }),
		b.eventBus.Subscribe(events.EventRunCompleted, func(e events.Event) { b.handleRunEvent(ctx, e) }),
		b.eventBus.Subscribe(events.EventRunFailed, func(e events.Event) { b.handleRunEvent(ctx, e) }),
		b.eventBus.Subscribe(events.EventLogLine, b.handleLogLineEvent),
	)
}

// Shutdown removes all event subscriptions.
func (b *EventBridge) Shutdown() {
	for _, unsubscribe := range b.unsubscribers {
		if unsubscribe != nil {
			unsubscribe()
		}
	}
	b.unsubscribers = nil
}

func (b *EventBridge) handleRunEvent(ctx context.Context, event events.Event) {
	runEvent, ok := event.Data.(events.RunEvent)
	if !ok || runEvent.Run == nil || runEvent.Run.ExternalExecutionID == nil {
		return
	}

	run := runEvent.Run
	executionID := *run.ExternalExecutionID
	update := mapRunToExecutionUpdate(run)
	if update == nil {
		return
	}

	terminal := run.Status.IsTerminal()

	if run.Status == model.PhaseRunning {
		b.tracker.TrackRunning(executionID, run.StartAt)
	}
	if terminal {
		b.tracker.Remove(executionID)
	}
	b.onStateChange()

	if !terminal {
		b.tracker.QueueUpdate(*update, b.sendReady)
		return
	}

	// Terminal path: archive the log first (off the publishing goroutine so
	// the runtime's `execute` returns promptly and frees the concurrency
	// slot), then emit the terminal update with logPath/logSize set.
	go b.finalizeRun(ctx, run, *update, executionID)
}

func (b *EventBridge) finalizeRun(ctx context.Context, run *model.Run, update protocol.ExecutionUpdateMessage, executionID string) {
	uploader := b.handler.Uploader()
	if uploader != nil {
		logFilePath := logutil.ResolveRunLogPath(b.handler.LogDir(), run.TaskName, run.ID, run.CreatedAt)
		result, err := uploader.Archive(ctx, executionID, logFilePath)
		switch {
		case err != nil:
			slog.Warn("log archival failed; sending terminal update without logPath", "executionId", executionID, "err", err)
		case result != nil:
			update.LogPath = result.LogPath
			update.LogSize = result.LogSize
		}
	}

	b.tracker.QueueUpdate(update, b.sendReady)
	b.handler.RemoveLogListener(executionID)
}

func (b *EventBridge) handleLogLineEvent(event events.Event) {
	logEvent, ok := event.Data.(events.LogLineEvent)
	if !ok || logEvent.ExternalExecutionID == "" {
		return
	}
	if !b.handler.IsLogListener(logEvent.ExternalExecutionID) {
		return
	}
	message := NewLogLineMessage(
		logEvent.ExternalExecutionID,
		logEvent.LineNum,
		logEvent.Timestamp,
		logEvent.Stream,
		logEvent.Text,
		logEvent.Continued,
	)
	// Best-effort: a slow peer with a full outbound queue must not block the
	// run. The handler caller (events.Bus.Publish) is sync — the outbound
	// channel is bounded, so sendReady drops with an error rather than
	// pushing back. We intentionally swallow that error: backpressure is
	// surfaced as a "missed line" on the cloud side, never a stalled run.
	_ = b.sendReady(message)
}
