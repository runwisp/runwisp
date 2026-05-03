// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"context"
	"encoding/base64"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"log/slog"
)

// EventBridge subscribes to runtime events and forwards execution status
// and log data to the cloud connection.
type EventBridge struct {
	eventBus      events.EventBus
	handler       *InboundHandler
	tracker       *ExecutionTracker
	sendReady     func(any) error
	onStateChange func()
	unsubscribers []func()
}

func NewEventBridge(
	eventBus events.EventBus,
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

// Subscribe registers event listeners on the event bus.
func (b *EventBridge) Subscribe() {
	b.unsubscribers = append(b.unsubscribers,
		b.eventBus.Subscribe(events.EventRunStarted, b.handleRunEvent),
		b.eventBus.Subscribe(events.EventRunCompleted, b.handleRunEvent),
		b.eventBus.Subscribe(events.EventRunFailed, b.handleRunEvent),
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

func (b *EventBridge) handleRunEvent(event events.Event) {
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
	go b.finalizeRun(run, *update, executionID)
}

func (b *EventBridge) finalizeRun(run *model.Run, update protocol.ExecutionUpdateMessage, executionID string) {
	uploader := b.handler.Uploader()
	if uploader != nil {
		logFilePath := logutil.ResolveRunLogPath(b.handler.LogDir(), run.TaskName, run.ID, run.CreatedAt)
		result, err := uploader.Archive(context.Background(), executionID, logFilePath)
		switch {
		case err != nil:
			slog.Warn("log archival failed; sending terminal update without logPath", "executionId", executionID, "err", err)
		case result != nil:
			update.LogPath = result.LogPath
			update.LogSize = result.LogSize
		}
	}

	b.tracker.QueueUpdate(update, b.sendReady)
	b.sendTerminalLogChunk(executionID)
}

func (b *EventBridge) handleLogLineEvent(event events.Event) {
	logEvent, ok := event.Data.(events.LogLineEvent)
	if !ok || logEvent.ExternalExecutionID == "" {
		return
	}

	chunk, exists := b.handler.BufferLogChunk(logEvent.ExternalExecutionID, []byte(logEvent.Line), b.emitLogChunk)
	if !exists || !chunk.Ready {
		return
	}
	b.emitLogChunk(logEvent.ExternalExecutionID, chunk.Offset, chunk.Data)
}

func (b *EventBridge) emitLogChunk(executionID string, offset int64, data []byte) {
	if len(data) == 0 {
		return
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	message := NewLogChunkMessage(executionID, encoded, offset, false)
	if err := b.sendReady(message); err != nil {
		slog.Warn("failed to send log chunk", "executionID", executionID, "offset", offset, "err", err)
	}
}

func (b *EventBridge) sendTerminalLogChunk(executionID string) {
	offset, pending, exists := b.handler.RemoveLogListener(executionID)
	if !exists {
		return
	}

	if len(pending) > 0 {
		// Drain whatever was buffered for the rate-limit window. The final
		// chunk follows with Final=true at the post-buffer offset.
		encoded := base64.StdEncoding.EncodeToString(pending)
		buffered := NewLogChunkMessage(executionID, encoded, offset, false)
		if err := b.sendReady(buffered); err != nil {
			slog.Info("failed to flush buffered log chunk", "executionID", executionID, "err", err)
		}
		offset += int64(len(pending))
	}

	message := NewLogChunkMessage(executionID, "", offset, true)
	if err := b.sendReady(message); err != nil {
		slog.Info("failed to send final log chunk", "executionID", executionID, "err", err)
	}
}
