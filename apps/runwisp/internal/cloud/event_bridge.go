// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"encoding/base64"

	"github.com/runwisp/runwisp/internal/events"
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

	b.tracker.QueueUpdate(*update, b.sendReady)

	if terminal {
		b.sendTerminalLogChunk(executionID)
	}
}

func (b *EventBridge) handleLogLineEvent(event events.Event) {
	logEvent, ok := event.Data.(events.LogLineEvent)
	if !ok || logEvent.ExternalExecutionID == "" {
		return
	}

	chunkBytes := []byte(logEvent.Line)
	// ClaimLogChunkOffset atomically reserves the offset range for this chunk,
	// so concurrent goroutines (stdout/stderr) always get non-overlapping slots.
	offset, exists := b.handler.ClaimLogChunkOffset(logEvent.ExternalExecutionID, int64(len(chunkBytes)))
	if !exists {
		return
	}

	encoded := base64.StdEncoding.EncodeToString(chunkBytes)
	message := NewLogChunkMessage(logEvent.ExternalExecutionID, encoded, offset, false)
	if err := b.sendReady(message); err != nil {
		slog.Warn("failed to send log chunk", "executionID", logEvent.ExternalExecutionID, "offset", offset, "err", err)
	}
}

func (b *EventBridge) sendTerminalLogChunk(executionID string) {
	offset, exists := b.handler.RemoveLogListener(executionID)
	if !exists {
		return
	}

	message := NewLogChunkMessage(executionID, "", offset, true)
	if err := b.sendReady(message); err != nil {
		slog.Info("failed to send final log chunk", "executionID", executionID, "err", err)
	}
}
