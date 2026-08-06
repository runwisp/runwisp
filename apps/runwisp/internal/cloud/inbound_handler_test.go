// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cloud

import (
	"context"
	"errors"
	"testing"

	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestInboundHandler() *InboundHandler {
	return NewInboundHandler(InboundHandlerDeps{
		LogDir:          "/tmp/logs",
		QueueExecUpdate: func(protocol.ExecutionUpdateMessage) {},
	})
}

// stubRunRepo implements ExternalRunGetter for inbound handler tests.
type stubRunRepo struct {
	run    *model.Run
	getErr error
}

func (f *stubRunRepo) GetRunByExternalExecutionID(_ context.Context, _ string) (*model.Run, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.run == nil {
		return nil, ErrNotFound
	}
	return f.run, nil
}

func newDispatchInboundHandler(runner TaskRunner, repo ExternalRunGetter, avail executor.Availability) *InboundHandler {
	return &InboundHandler{
		taskManager:     runner,
		runRepo:         repo,
		logDir:          "/tmp/logs",
		availability:    avail,
		queueExecUpdate: func(protocol.ExecutionUpdateMessage) {},
		logListeners:    make(map[string]struct{}),
	}
}

// --- HandleExecutionDispatch ---

func TestHandleExecutionDispatch_EmptyExecutionID(t *testing.T) {
	h := newTestInboundHandler()
	acked := false
	err := h.HandleExecutionDispatch(context.Background(), protocol.ExecutionDispatchMessage{
		Execution: &protocol.Execution{ExecutionID: ""},
	}, func() { acked = true })
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindValidation, ce.Kind)
	assert.False(t, acked, "invalid dispatch must not be acked")
}

func TestHandleExecutionDispatch_InvalidScript(t *testing.T) {
	avail := executor.Availability{Shell: executor.BackendStatus{Available: true}}
	runner := &fakeTaskRunner{tasks: make(map[string]*model.Task)}
	h := newDispatchInboundHandler(runner, nil, avail)

	err := h.HandleExecutionDispatch(context.Background(), protocol.ExecutionDispatchMessage{
		Execution: &protocol.Execution{
			ExecutionID: "exec-1",
			Script:      []byte(`{bad json`),
		},
	}, nil)
	require.Error(t, err)
}

func TestHandleExecutionDispatch_Success(t *testing.T) {
	avail := executor.Availability{Shell: executor.BackendStatus{Available: true}}
	runner := &fakeTaskRunner{tasks: make(map[string]*model.Task)}
	h := newDispatchInboundHandler(runner, nil, avail)

	script := shellScript(t, "echo hello")
	acked := 0
	err := h.HandleExecutionDispatch(context.Background(), protocol.ExecutionDispatchMessage{
		Execution: &protocol.Execution{
			ExecutionID: "exec-abc",
			TaskID:      "my-task",
			Script:      script,
		},
	}, func() { acked++ })
	require.NoError(t, err)
	assert.Equal(t, 1, acked, "valid dispatch must be acked exactly once")
}

func TestHandleExecutionDispatch_TriggerError_NilRun(t *testing.T) {
	avail := executor.Availability{Shell: executor.BackendStatus{Available: true}}
	runner := &fakeTaskRunner{
		tasks:   make(map[string]*model.Task),
		trigErr: errors.New("queue full"),
		trigRun: nil,
	}
	h := newDispatchInboundHandler(runner, nil, avail)

	script := shellScript(t, "echo hi")
	err := h.HandleExecutionDispatch(context.Background(), protocol.ExecutionDispatchMessage{
		Execution: &protocol.Execution{
			ExecutionID: "exec-fail",
			TaskID:      "some-task",
			Script:      script,
		},
	}, nil)
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindConflict, ce.Kind)
}

func TestHandleExecutionDispatch_TriggerError_WithRun(t *testing.T) {
	avail := executor.Availability{Shell: executor.BackendStatus{Available: true}}
	run := &model.Run{ID: "r1", Status: model.PhaseEnded}
	runner := &fakeTaskRunner{
		tasks:   make(map[string]*model.Task),
		trigErr: errors.New("conflict"),
		trigRun: run,
	}
	h := newDispatchInboundHandler(runner, nil, avail)

	script := shellScript(t, "echo hi")
	err := h.HandleExecutionDispatch(context.Background(), protocol.ExecutionDispatchMessage{
		Execution: &protocol.Execution{
			ExecutionID: "exec-conflict",
			TaskID:      "task",
			Script:      script,
		},
	}, nil)
	require.Error(t, err)
}

func TestHandleExecutionDispatch_DuplicateActive_ReAcksWithoutTrigger(t *testing.T) {
	avail := executor.Availability{Shell: executor.BackendStatus{Available: true}}
	runner := &fakeTaskRunner{tasks: make(map[string]*model.Task)}
	tracker := NewExecutionTracker()
	tracker.TrackRunning("exec-dup", nil)
	h := newDispatchInboundHandler(runner, nil, avail)
	h.tracker = tracker

	script := shellScript(t, "echo hi")
	acked := 0
	err := h.HandleExecutionDispatch(context.Background(), protocol.ExecutionDispatchMessage{
		Execution: &protocol.Execution{
			ExecutionID: "exec-dup",
			TaskID:      "task",
			Script:      script,
		},
	}, func() { acked++ })
	require.NoError(t, err)
	assert.Equal(t, 1, acked, "duplicate dispatch must be re-acked")
	assert.Empty(t, runner.triggered, "duplicate dispatch must not trigger a second run")
}

func TestHandleExecutionDispatch_DuplicateTerminal_ReQueuesTerminalUpdate(t *testing.T) {
	avail := executor.Availability{Shell: executor.BackendStatus{Available: true}}
	runner := &fakeTaskRunner{tasks: make(map[string]*model.Task)}
	reason := model.ReasonSuccess
	execID := "exec-done"
	repo := &stubRunRepo{run: &model.Run{
		ID:                  "r1",
		Status:              model.PhaseEnded,
		EndReason:           &reason,
		ExternalExecutionID: &execID,
	}}
	h := newDispatchInboundHandler(runner, repo, avail)

	var updates []protocol.ExecutionUpdateMessage
	h.queueExecUpdate = func(u protocol.ExecutionUpdateMessage) { updates = append(updates, u) }

	script := shellScript(t, "echo hi")
	acked := 0
	err := h.HandleExecutionDispatch(context.Background(), protocol.ExecutionDispatchMessage{
		Execution: &protocol.Execution{
			ExecutionID: execID,
			TaskID:      "task",
			Script:      script,
		},
	}, func() { acked++ })
	require.NoError(t, err)
	assert.Equal(t, 1, acked)
	assert.Empty(t, runner.triggered, "terminal duplicate must not re-run")
	require.Len(t, updates, 1, "terminal duplicate must re-queue the stored terminal update")
	assert.Equal(t, execID, updates[0].ExecutionID)
}

// --- HandleExecutionStop ---

func TestHandleExecutionStop_EmptyID(t *testing.T) {
	h := newTestInboundHandler()
	err := h.HandleExecutionStop(context.Background(), protocol.ExecutionStopMessage{ExecutionID: ""})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindValidation, ce.Kind)
}

func TestHandleExecutionStop_NotFound(t *testing.T) {
	repo := &stubRunRepo{getErr: ErrNotFound}
	runner := &fakeTaskRunner{}
	h := newDispatchInboundHandler(runner, repo, executor.Availability{})

	err := h.HandleExecutionStop(context.Background(), protocol.ExecutionStopMessage{ExecutionID: "exec-1"})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindUnknownExecution, ce.Kind)
}

func TestHandleExecutionStop_RunAlreadyTerminal(t *testing.T) {
	reason := model.ReasonSuccess
	run := &model.Run{Status: model.PhaseEnded, EndReason: &reason}
	repo := &stubRunRepo{run: run}
	runner := &fakeTaskRunner{}
	h := newDispatchInboundHandler(runner, repo, executor.Availability{})

	err := h.HandleExecutionStop(context.Background(), protocol.ExecutionStopMessage{ExecutionID: "exec-1"})
	require.NoError(t, err)
}

func TestHandleExecutionStop_RunActive_NotRunning(t *testing.T) {
	run := &model.Run{Status: model.PhasePending}
	repo := &stubRunRepo{run: run}
	runner := &fakeTaskRunner{}
	h := newDispatchInboundHandler(runner, repo, executor.Availability{})

	err := h.HandleExecutionStop(context.Background(), protocol.ExecutionStopMessage{ExecutionID: "exec-1"})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindConflict, ce.Kind)
}

func TestHandleExecutionStop_RepoTransientError(t *testing.T) {
	repo := &stubRunRepo{getErr: errors.New("db error")}
	runner := &fakeTaskRunner{}
	h := newDispatchInboundHandler(runner, repo, executor.Availability{})

	err := h.HandleExecutionStop(context.Background(), protocol.ExecutionStopMessage{ExecutionID: "exec-1"})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindTransient, ce.Kind)
}

// --- HandleLogReplayRequest ---

func TestHandleLogReplayRequest_EmptyID(t *testing.T) {
	h := newTestInboundHandler()
	_, err := h.HandleLogReplayRequest(context.Background(), protocol.LogReplayRequestMessage{ExecutionID: ""})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindValidation, ce.Kind)
}

func TestHandleLogReplayRequest_NotFound(t *testing.T) {
	repo := &stubRunRepo{getErr: ErrNotFound}
	h := newDispatchInboundHandler(nil, repo, executor.Availability{})

	chunk, err := h.HandleLogReplayRequest(context.Background(), protocol.LogReplayRequestMessage{
		RequestID:   "req-1",
		ExecutionID: "exec-1",
	})
	require.NoError(t, err)
	assert.False(t, chunk.Final, "unknown execution must not claim end-of-log — the dispatch may not have arrived yet")
}

func TestHandleLogReplayRequest_TransientError(t *testing.T) {
	repo := &stubRunRepo{getErr: errors.New("db timeout")}
	h := newDispatchInboundHandler(nil, repo, executor.Availability{})

	_, err := h.HandleLogReplayRequest(context.Background(), protocol.LogReplayRequestMessage{
		RequestID:   "req-1",
		ExecutionID: "exec-1",
	})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindTransient, ce.Kind)
}

// TestInboundHandler_FreshHandlerGetters covers the four "zero-state" getter
// branches in one place: LogDir/Uploader propagate from construction; the
// listener queries return false because no listener was registered yet.
func TestInboundHandler_FreshHandlerGetters(t *testing.T) {
	h := newTestInboundHandler()
	assert.Equal(t, "/tmp/logs", h.LogDir())
	assert.Nil(t, h.Uploader(), "uploader must be nil when not configured")
	assert.False(t, h.IsLogListener("exec-1"), "no listener registered → must be false")
	assert.NotPanics(t, func() { h.RemoveLogListener("exec-1") }, "removing an absent listener must be a no-op")
}

func TestInboundHandler_HandleLogListen_And_IsLogListener(t *testing.T) {
	h := newTestInboundHandler()
	err := h.HandleLogListen(protocol.LogListenMessage{ExecutionID: "exec-1"})
	require.NoError(t, err)
	assert.True(t, h.IsLogListener("exec-1"))
}

func TestInboundHandler_HandleLogListen_EmptyID_Error(t *testing.T) {
	h := newTestInboundHandler()
	err := h.HandleLogListen(protocol.LogListenMessage{ExecutionID: ""})
	require.Error(t, err)
	assert.False(t, h.IsLogListener(""))
}

func TestInboundHandler_HandleLogListen_Idempotent(t *testing.T) {
	h := newTestInboundHandler()
	_ = h.HandleLogListen(protocol.LogListenMessage{ExecutionID: "exec-1"})
	_ = h.HandleLogListen(protocol.LogListenMessage{ExecutionID: "exec-1"})
	assert.True(t, h.IsLogListener("exec-1"))
}

func TestInboundHandler_RemoveLogListener_Present(t *testing.T) {
	h := newTestInboundHandler()
	_ = h.HandleLogListen(protocol.LogListenMessage{ExecutionID: "exec-1"})
	h.RemoveLogListener("exec-1")
	assert.False(t, h.IsLogListener("exec-1"))
}

func TestInboundHandler_HandleLogStop_RemovesListener(t *testing.T) {
	h := newTestInboundHandler()
	_ = h.HandleLogListen(protocol.LogListenMessage{ExecutionID: "exec-1"})
	h.HandleLogStop(protocol.LogStopMessage{ExecutionID: "exec-1"})
	assert.False(t, h.IsLogListener("exec-1"))
}

func TestInboundHandler_HandleLogStop_EmptyID_NoOp(t *testing.T) {
	h := newTestInboundHandler()
	h.HandleLogStop(protocol.LogStopMessage{ExecutionID: ""}) // must not panic
}

func TestInboundHandler_ClearLogListeners(t *testing.T) {
	h := newTestInboundHandler()
	_ = h.HandleLogListen(protocol.LogListenMessage{ExecutionID: "exec-1"})
	_ = h.HandleLogListen(protocol.LogListenMessage{ExecutionID: "exec-2"})
	h.ClearLogListeners()
	assert.False(t, h.IsLogListener("exec-1"))
	assert.False(t, h.IsLogListener("exec-2"))
}

func TestDecodeInboundMessage_AuthResult(t *testing.T) {
	payload := []byte(`{"type":"auth:result","success":true,"connectionId":"conn-1"}`)

	decoded, err := DecodeInboundMessage(payload)
	require.NoError(t, err)

	message, ok := decoded.(protocol.AuthResultMessage)
	require.True(t, ok)
	assert.True(t, message.Success)
	assert.Equal(t, "conn-1", message.ConnectionID)
}

func TestInboundHandler_HandleAgentRestart(t *testing.T) {
	t.Run("nil requester is rejected as conflict", func(t *testing.T) {
		h := newTestInboundHandler() // constructed with a nil restart callback
		err := h.HandleAgentRestart()
		var cloudErr *CloudError
		require.ErrorAs(t, err, &cloudErr)
		assert.Equal(t, CloudErrorKindConflict, cloudErr.Kind)
	})

	t.Run("requester error surfaces as conflict", func(t *testing.T) {
		h := NewInboundHandler(InboundHandlerDeps{
			LogDir:          "/tmp/logs",
			QueueExecUpdate: func(protocol.ExecutionUpdateMessage) {},
			RequestRestart:  func() error { return errors.New("not service-managed") },
		})
		err := h.HandleAgentRestart()
		var cloudErr *CloudError
		require.ErrorAs(t, err, &cloudErr)
		assert.Equal(t, CloudErrorKindConflict, cloudErr.Kind)
		assert.Contains(t, cloudErr.Message, "not service-managed")
	})

	t.Run("success invokes the requester once", func(t *testing.T) {
		calls := 0
		h := NewInboundHandler(InboundHandlerDeps{
			LogDir:          "/tmp/logs",
			QueueExecUpdate: func(protocol.ExecutionUpdateMessage) {},
			RequestRestart:  func() error { calls++; return nil },
		})
		require.NoError(t, h.HandleAgentRestart())
		assert.Equal(t, 1, calls)
	})
}
