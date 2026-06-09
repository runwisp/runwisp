// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/executor"
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

	// Register listener so LogLine events get forwarded.
	_ = h.HandleLogListen(protocol.LogListenMessage{ExecutionID: extID})
	bus.Publish(events.EventLogLine, events.LogLineEvent{
		ExternalExecutionID: extID,
		LineNum:             1,
		Text:                "hi",
	})

	// 3 run events → 3 state-change callbacks (sync). Log line sends sync.
	assert.Equal(t, 3, stateChanges)
	assert.True(t, logLineSent)
}

// --- handleRunEvent: guard branches ---

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

// --- finalizeRun: uploader success branch ---

// inboundHandlerWithUploader builds an InboundHandler whose Uploader() returns
// a real LogUploader. finalizeRun's success path resolves the run's on-disk log
// path via the handler's LogDir(), archives it, and overlays LogPath/LogSize
// onto the outbound update. Covering this branch requires both an HTTP fixture
// for the gzip+PUT and a pre-registered dispatch entry.
func inboundHandlerWithUploader(t *testing.T, uploader *LogUploader, logDir string) *InboundHandler {
	t.Helper()
	return NewInboundHandler(
		nil, nil, logDir,
		executor.Availability{},
		func(protocol.ExecutionUpdateMessage) {},
		uploader,
	)
}

func TestEventBridge_FinalizeRun_UploaderSuccess_OverlaysLogPathAndSize(t *testing.T) {
	logDir := t.TempDir()
	run := newTerminalRun("greet", "01HX-RUN-FIN", "exec-fin-ok")
	writeRunLog(t, logDir, run, "log contents for archive\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	repo := newFakePendingRepo()
	runs := &fakeRunRepo{byExt: map[string]*model.Run{"exec-fin-ok": run}}
	uploader := NewLogUploader(repo, runs, logDir, fixedClock())
	uploader.httpClient = srv.Client()

	require.NoError(t, uploader.RegisterDispatch(context.Background(), "exec-fin-ok", srv.URL, "archive/exec-fin-ok.log.gz"))

	h := inboundHandlerWithUploader(t, uploader, logDir)
	// Register a log listener so finalizeRun's cleanup path is exercised.
	_ = h.HandleLogListen(protocol.LogListenMessage{ExecutionID: "exec-fin-ok"})

	var captured protocol.ExecutionUpdateMessage
	var sent int
	tracker := NewExecutionTracker()
	b := NewEventBridge(
		events.NewEventBus(),
		h,
		tracker,
		func(message any) error {
			if upd, ok := message.(protocol.ExecutionUpdateMessage); ok {
				captured = upd
				sent++
			}
			return nil
		},
		func() {},
	)

	reason := model.ReasonSuccess
	extID := "exec-fin-ok"
	run.EndReason = &reason
	update := protocol.ExecutionUpdateMessage{ExecutionID: extID}

	b.finalizeRun(context.Background(), run, update, extID)

	assert.Equal(t, 1, sent, "finalizeRun must queue exactly one terminal update")
	assert.Equal(t, "archive/exec-fin-ok.log.gz", captured.LogPath, "LogPath must be overlaid from uploader result")
	assert.Greater(t, captured.LogSize, int64(0), "LogSize must be overlaid from uploader result")
	assert.False(t, h.IsLogListener(extID), "log listener must be removed after finalize")
	assert.Equal(t, 0, repo.count(), "successful upload must drop the pending row")
}

// finalizeRun's archive-error branch logs and falls through, sending the
// update untouched. We cover it by pointing the uploader at a server that
// returns 500 — Archive bubbles that up, finalizeRun swallows it.
func TestEventBridge_FinalizeRun_UploaderError_SendsUpdateWithoutLogPath(t *testing.T) {
	logDir := t.TempDir()
	run := newTerminalRun("greet", "01HX-RUN-ERR", "exec-fin-err")
	writeRunLog(t, logDir, run, "log\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// 4xx returns a PermanentError — no retries, keeping the test fast.
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	repo := newFakePendingRepo()
	runs := &fakeRunRepo{byExt: map[string]*model.Run{"exec-fin-err": run}}
	uploader := NewLogUploader(repo, runs, logDir, fixedClock())
	uploader.httpClient = srv.Client()

	require.NoError(t, uploader.RegisterDispatch(context.Background(), "exec-fin-err", srv.URL, "archive/exec-fin-err.log.gz"))

	h := inboundHandlerWithUploader(t, uploader, logDir)

	var captured protocol.ExecutionUpdateMessage
	var sent int
	b := NewEventBridge(
		events.NewEventBus(),
		h,
		NewExecutionTracker(),
		func(message any) error {
			if upd, ok := message.(protocol.ExecutionUpdateMessage); ok {
				captured = upd
				sent++
			}
			return nil
		},
		func() {},
	)

	reason := model.ReasonFailed
	extID := "exec-fin-err"
	run.EndReason = &reason
	update := protocol.ExecutionUpdateMessage{ExecutionID: extID}

	b.finalizeRun(context.Background(), run, update, extID)

	assert.Equal(t, 1, sent, "update is queued even on archive failure")
	assert.Equal(t, "", captured.LogPath, "LogPath stays empty when archive fails")
	assert.Equal(t, int64(0), captured.LogSize, "LogSize stays zero when archive fails")
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
