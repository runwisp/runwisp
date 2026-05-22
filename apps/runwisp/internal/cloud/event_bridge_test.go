// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"context"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
	"github.com/stretchr/testify/assert"
)

func newTestBridge(h *InboundHandler) *EventBridge {
	bus := events.NewEventBus()
	return NewEventBridge(
		bus,
		h,
		NewExecutionTracker(),
		func(any) error { return nil },
		func() {},
	)
}

// recordingBus is a fake EventBus that records every Subscribe call's
// event type and tracks how many of those subscriptions have been
// unsubscribed. SubscribeAll is unused by the bridge today.
type recordingBus struct {
	subscribed   []events.EventType
	unsubscribed int
}

func (r *recordingBus) Subscribe(et events.EventType, _ events.EventHandler) func() {
	r.subscribed = append(r.subscribed, et)
	return func() { r.unsubscribed++ }
}
func (r *recordingBus) SubscribeAll(_ events.EventHandler) func()      { return func() {} }
func (r *recordingBus) Publish(_ events.EventType, _ events.EventData) {}

// --- Start / Shutdown subscription lifecycle ---

func TestEventBridge_StartShutdown_SubscribesAndUnsubscribesEveryKind(t *testing.T) {
	bus := &recordingBus{}
	b := NewEventBridge(
		bus,
		newTestInboundHandler(),
		NewExecutionTracker(),
		func(any) error { return nil },
		func() {},
	)

	b.Start(context.Background())
	assert.ElementsMatch(t,
		[]events.EventType{
			events.EventRunStarted,
			events.EventRunCompleted,
			events.EventRunFailed,
			events.EventLogLine,
		},
		bus.subscribed,
		"bridge must subscribe to exactly the four run/log event kinds")

	b.Shutdown()
	assert.Equal(t, len(bus.subscribed), bus.unsubscribed,
		"Shutdown must call every unsubscribe returned by Subscribe")
}

// --- handleRunEvent ---

func TestEventBridge_HandleRunEvent_RunningRun(t *testing.T) {
	stateChanges := 0
	h := newTestInboundHandler()
	b := NewEventBridge(
		events.NewEventBus(),
		h,
		NewExecutionTracker(),
		func(any) error { return nil },
		func() { stateChanges++ },
	)

	extID := "exec-running"
	now := time.Now()
	run := &sqlcdb.Run{
		ID:                  "r1",
		TaskName:            "t1",
		Status:              sqlcdb.PhaseRunning,
		ExternalExecutionID: &extID,
		StartAt:             &now,
	}
	b.handleRunEvent(context.Background(), events.Event{Data: events.RunEvent{Run: run}})
	assert.Equal(t, 1, stateChanges)
	assert.True(t, b.tracker.HasActive())
}

func TestEventBridge_HandleRunEvent_TerminalRun(t *testing.T) {
	stateChanges := 0
	h := newTestInboundHandler()
	b := NewEventBridge(
		events.NewEventBus(),
		h,
		NewExecutionTracker(),
		func(any) error { return nil },
		func() { stateChanges++ },
	)

	extID := "exec-done"
	reason := sqlcdb.ReasonSuccess
	run := &sqlcdb.Run{
		ID:                  "r1",
		TaskName:            "t1",
		Status:              sqlcdb.PhaseEnded,
		EndReason:           &reason,
		ExternalExecutionID: &extID,
	}
	b.handleRunEvent(context.Background(), events.Event{Data: events.RunEvent{Run: run}})
	assert.Equal(t, 1, stateChanges)
}

// --- handleLogLineEvent ---

func TestEventBridge_HandleLogLineEvent_IsListener(t *testing.T) {
	h := newTestInboundHandler()
	sent := 0
	b := NewEventBridge(
		events.NewEventBus(),
		h,
		NewExecutionTracker(),
		func(any) error { sent++; return nil },
		func() {},
	)

	execID := "exec-listen"
	_ = h.HandleLogListen(protocol.LogListenMessage{ExecutionID: execID})
	b.handleLogLineEvent(events.Event{
		Data: events.LogLineEvent{
			ExternalExecutionID: execID,
			LineNum:             1,
			Text:                "hello",
		},
	})
	assert.Equal(t, 1, sent)
}
