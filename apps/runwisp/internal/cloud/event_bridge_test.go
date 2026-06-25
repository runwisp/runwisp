// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	run := &model.Run{
		ID:                  "r1",
		TaskName:            "t1",
		Status:              model.PhaseRunning,
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
	reason := model.ReasonSuccess
	run := &model.Run{
		ID:                  "r1",
		TaskName:            "t1",
		Status:              model.PhaseEnded,
		EndReason:           &reason,
		ExternalExecutionID: &extID,
	}
	b.handleRunEvent(context.Background(), events.Event{Data: events.RunEvent{Run: run}})
	assert.Equal(t, 1, stateChanges)
}

// --- Start delivers events through the real bus ---

func TestEventBridge_Start_DispatchesPublishedEvents(t *testing.T) {
	bus := events.NewEventBus()
	h := newTestInboundHandler()
	stateChanges := 0
	logLineSent := false
	b := NewEventBridge(
		bus,
		h,
		NewExecutionTracker(),
		func(message any) error {
			if _, ok := message.(protocol.LogLineMessage); ok {
				logLineSent = true
			}
			return nil
		},
		func() { stateChanges++ },
	)
	b.Start(context.Background())
	defer b.Shutdown()

	extID := "exec-pub"
	now := time.Now()
	runStarted := &model.Run{
		ID: "r1", TaskName: "t", Status: model.PhaseRunning,
		ExternalExecutionID: &extID, StartAt: &now,
	}
	bus.Publish(events.EventRunStarted, events.RunEvent{Run: runStarted})

	reason := model.ReasonSuccess
	runDone := &model.Run{
		ID: "r1", TaskName: "t", Status: model.PhaseEnded,
		EndReason: &reason, ExternalExecutionID: &extID,
	}
	bus.Publish(events.EventRunCompleted, events.RunEvent{Run: runDone})

	failReason := model.ReasonFailed
	runFail := &model.Run{
		ID: "r2", TaskName: "t", Status: model.PhaseEnded,
		EndReason: &failReason, ExternalExecutionID: &extID,
	}
	bus.Publish(events.EventRunFailed, events.RunEvent{Run: runFail})

	// Register listener so LogLine events get forwarded. Use a distinct
	// execution ID: the terminal runs above each spawn a finalizeRun
	// goroutine that calls RemoveLogListener(extID), which would otherwise
	// race this registration and drop the log line.
	logExtID := "exec-log"
	_ = h.HandleLogListen(protocol.LogListenMessage{ExecutionID: logExtID})
	bus.Publish(events.EventLogLine, events.LogLineEvent{
		ExternalExecutionID: logExtID,
		LineNum:             1,
		Text:                "hi",
	})

	// 3 run events → 3 state-change callbacks (sync). Log line sends sync.
	assert.Equal(t, 3, stateChanges)
	assert.True(t, logLineSent)
}

// --- handleRunEvent: guard branches ---

// A service-instance lifecycle run carries no external execution id; the bridge
// pushes the supervisor snapshot keyed by the bare task name. This is the path a
// supervised TOML service (now running in cloud mode) takes — an old cloud drops
// the bare-name frame, a new cloud resolves it.
func TestEventBridge_HandleRunEvent_EmitsServiceStatusByBareName(t *testing.T) {
	h := newDispatchHandler(shellAvailable(), nil)
	runner := h.taskManager.(*fakeTaskRunner)
	runner.snapshot = model.ServiceSnapshot{
		TaskName:         "heartbeat",
		State:            "running",
		DesiredInstances: 2,
		RunningInstances: 2,
	}
	runner.snapshotOK = true

	var sent []any
	b := NewEventBridge(
		events.NewEventBus(),
		h,
		NewExecutionTracker(),
		func(msg any) error { sent = append(sent, msg); return nil },
		func() {},
	)

	run := &model.Run{TaskName: "heartbeat"} // no ExternalExecutionID
	b.handleRunEvent(context.Background(), events.Event{Data: events.RunEvent{Run: run}})

	require.Len(t, sent, 1)
	msg, ok := sent[0].(protocol.ServiceStatusMessage)
	require.True(t, ok)
	assert.Equal(t, "heartbeat", msg.TaskID, "service:status is keyed by the bare TOML name")
	assert.Equal(t, 2, msg.DesiredInstances)
}

func TestEventBridge_HandleRunEvent_IgnoresWrongData(t *testing.T) {
	b := newTestBridge(newTestInboundHandler())
	// Data is not events.RunEvent — must return without panicking.
	b.handleRunEvent(context.Background(), events.Event{Data: events.LogLineEvent{}})
}

func TestEventBridge_HandleRunEvent_IgnoresNilRun(t *testing.T) {
	b := newTestBridge(newTestInboundHandler())
	b.handleRunEvent(context.Background(), events.Event{Data: events.RunEvent{Run: nil}})
}

func TestEventBridge_HandleRunEvent_IgnoresMissingExternalID(t *testing.T) {
	b := newTestBridge(newTestInboundHandler())
	run := &model.Run{ID: "r", Status: model.PhaseRunning}
	b.handleRunEvent(context.Background(), events.Event{Data: events.RunEvent{Run: run}})
}

// --- finalizeRun: nil uploader short-circuit ---

func TestEventBridge_FinalizeRun_NilUploaderQueuesUpdateUnchanged(t *testing.T) {
	h := newTestInboundHandler() // uploader is nil
	sent := 0
	tracker := NewExecutionTracker()
	b := NewEventBridge(
		events.NewEventBus(),
		h,
		tracker,
		func(any) error { sent++; return nil },
		func() {},
	)

	extID := "exec-fin"
	_ = h.HandleLogListen(protocol.LogListenMessage{ExecutionID: extID})
	reason := model.ReasonSuccess
	run := &model.Run{
		ID: "r", TaskName: "t", Status: model.PhaseEnded,
		EndReason: &reason, ExternalExecutionID: &extID,
	}
	update := protocol.ExecutionUpdateMessage{}
	b.finalizeRun(context.Background(), run, update, extID)

	// Nil uploader → no archive, but tracker.QueueUpdate still called.
	assert.Equal(t, 1, sent)
	// LogListener should be cleaned up by finalizeRun.
	assert.False(t, h.IsLogListener(extID))
}

// --- handleLogLineEvent: ignore branches ---

func TestEventBridge_HandleLogLineEvent_IgnoresWrongData(t *testing.T) {
	b := newTestBridge(newTestInboundHandler())
	b.handleLogLineEvent(events.Event{Data: events.RunEvent{}})
}

func TestEventBridge_HandleLogLineEvent_IgnoresMissingExecID(t *testing.T) {
	b := newTestBridge(newTestInboundHandler())
	b.handleLogLineEvent(events.Event{Data: events.LogLineEvent{ExternalExecutionID: ""}})
}

func TestEventBridge_HandleLogLineEvent_IgnoresNonListenerExec(t *testing.T) {
	sent := 0
	b := NewEventBridge(
		events.NewEventBus(),
		newTestInboundHandler(),
		NewExecutionTracker(),
		func(any) error { sent++; return nil },
		func() {},
	)
	// No HandleLogListen — exec is not registered.
	b.handleLogLineEvent(events.Event{Data: events.LogLineEvent{ExternalExecutionID: "x", LineNum: 1}})
	assert.Equal(t, 0, sent)
}

// --- finalizeRun (terminal-before-archive ordering) ---

// TestEventBridge_FinalizeRun_TerminalBeforeArchiveThenAttach pins the
// fast-finish contract: the terminal update leaves before the archive PUT
// starts, a second identical update carrying logPath/logSize follows a
// successful upload, and the log listener is removed only after both.
func TestEventBridge_FinalizeRun_TerminalBeforeArchiveThenAttach(t *testing.T) {
	logDir := t.TempDir()
	execID := "exec-finalize"
	reason := model.ReasonSuccess
	run := newTerminalRun("task-a", "run-1", execID)
	run.EndReason = &reason
	writeRunLog(t, logDir, run, "line 1\n")

	var mu sync.Mutex
	var journal []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		journal = append(journal, "upload")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	uploader := NewLogUploader(newFakePendingRepo(), &fakeRunRepo{}, logDir, fixedClock())
	if err := uploader.RegisterDispatch(context.Background(), execID, srv.URL, "logs/org/key.gz"); err != nil {
		t.Fatalf("RegisterDispatch: %v", err)
	}

	h := NewInboundHandler(InboundHandlerDeps{
		LogDir:          logDir,
		QueueExecUpdate: func(protocol.ExecutionUpdateMessage) {},
		Uploader:        uploader,
	})
	_ = h.HandleLogListen(protocol.LogListenMessage{ExecutionID: execID})

	var sent []protocol.ExecutionUpdateMessage
	sendReady := func(msg any) error {
		update, ok := msg.(protocol.ExecutionUpdateMessage)
		if !ok {
			return nil
		}
		mu.Lock()
		sent = append(sent, update)
		if update.LogPath == "" {
			journal = append(journal, "terminal-update")
		} else {
			journal = append(journal, "attach-update")
		}
		mu.Unlock()
		// The listener must survive both updates: live streaming keeps
		// working while the archive uploads; removal is strictly last.
		assert.True(t, h.IsLogListener(execID),
			"log listener removed before updates finished")
		return nil
	}

	b := NewEventBridge(events.NewEventBus(), h, NewExecutionTracker(), sendReady, func() {})

	update := mapRunToExecutionUpdate(run)
	if update == nil {
		t.Fatal("mapRunToExecutionUpdate returned nil for terminal run")
	}
	b.finalizeRun(context.Background(), run, *update, execID)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"terminal-update", "upload", "attach-update"}, journal,
		"terminal update must be sent before the archive PUT, attach after")
	if assert.Len(t, sent, 2) {
		assert.Empty(t, sent[0].LogPath, "first update must not carry archive coordinates")
		assert.Zero(t, sent[0].LogSize)
		assert.Equal(t, "logs/org/key.gz", sent[1].LogPath)
		assert.Positive(t, sent[1].LogSize)
		assert.Equal(t, sent[0].ExecutionID, sent[1].ExecutionID)
		assert.Equal(t, sent[0].Status, sent[1].Status,
			"attach update must repeat the same terminal status")
	}
	assert.False(t, h.IsLogListener(execID), "log listener must be removed at the end")
}

// TestEventBridge_FinalizeRun_ArchiveFailureSendsSingleUpdate pins the
// degraded path: the terminal update has already gone out by the time the
// upload fails permanently, no attach update follows, and the listener is
// still cleaned up.
func TestEventBridge_FinalizeRun_ArchiveFailureSendsSingleUpdate(t *testing.T) {
	logDir := t.TempDir()
	execID := "exec-upload-fails"
	reason := model.ReasonSuccess
	run := newTerminalRun("task-a", "run-1", execID)
	run.EndReason = &reason
	writeRunLog(t, logDir, run, "line 1\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden) // 4xx = permanent, no retry loop
	}))
	defer srv.Close()

	uploader := NewLogUploader(newFakePendingRepo(), &fakeRunRepo{}, logDir, fixedClock())
	if err := uploader.RegisterDispatch(context.Background(), execID, srv.URL, "logs/org/key.gz"); err != nil {
		t.Fatalf("RegisterDispatch: %v", err)
	}

	h := NewInboundHandler(InboundHandlerDeps{
		LogDir:          logDir,
		QueueExecUpdate: func(protocol.ExecutionUpdateMessage) {},
		Uploader:        uploader,
	})
	_ = h.HandleLogListen(protocol.LogListenMessage{ExecutionID: execID})

	var sent []protocol.ExecutionUpdateMessage
	b := NewEventBridge(events.NewEventBus(), h, NewExecutionTracker(), func(msg any) error {
		if update, ok := msg.(protocol.ExecutionUpdateMessage); ok {
			sent = append(sent, update)
		}
		return nil
	}, func() {})

	update := mapRunToExecutionUpdate(run)
	if update == nil {
		t.Fatal("mapRunToExecutionUpdate returned nil for terminal run")
	}
	b.finalizeRun(context.Background(), run, *update, execID)

	if assert.Len(t, sent, 1, "no attach update after a failed upload") {
		assert.Empty(t, sent[0].LogPath)
		assert.Zero(t, sent[0].LogSize)
	}
	assert.False(t, h.IsLogListener(execID))
}

// TestEventBridge_FinalizeRun_NoUploaderSendsSingleUpdate covers daemons
// dispatched without a logUploadURL (uploader nil on the handler): a single
// terminal update, no archive coordinates, listener cleaned up.
func TestEventBridge_FinalizeRun_NoUploaderSendsSingleUpdate(t *testing.T) {
	execID := "exec-no-uploader"
	reason := model.ReasonSuccess
	run := newTerminalRun("task-a", "run-1", execID)
	run.EndReason = &reason

	h := newTestInboundHandler()
	_ = h.HandleLogListen(protocol.LogListenMessage{ExecutionID: execID})

	var sent []protocol.ExecutionUpdateMessage
	b := NewEventBridge(events.NewEventBus(), h, NewExecutionTracker(), func(msg any) error {
		if update, ok := msg.(protocol.ExecutionUpdateMessage); ok {
			sent = append(sent, update)
		}
		return nil
	}, func() {})

	update := mapRunToExecutionUpdate(run)
	if update == nil {
		t.Fatal("mapRunToExecutionUpdate returned nil for terminal run")
	}
	b.finalizeRun(context.Background(), run, *update, execID)

	if assert.Len(t, sent, 1) {
		assert.Empty(t, sent[0].LogPath)
	}
	assert.False(t, h.IsLogListener(execID))
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
