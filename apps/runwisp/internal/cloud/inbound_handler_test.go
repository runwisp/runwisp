// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"errors"
	"testing"

	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestInboundHandler() *InboundHandler {
	return NewInboundHandler(
		nil, nil, "/tmp/logs",
		executor.Availability{},
		func(protocol.ExecutionUpdateMessage) {},
		nil,
	)
}

// stubRunRepo implements ExternalRunGetter for inbound handler tests.
type stubRunRepo struct {
	run    *sqlcdb.Run
	getErr error
}

func (f *stubRunRepo) GetRunByExternalExecutionID(_ string) (*sqlcdb.Run, error) {
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
	err := h.HandleExecutionDispatch(protocol.ExecutionDispatchMessage{
		Execution: &protocol.Execution{ExecutionID: ""},
	})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindValidation, ce.Kind)
}

func TestHandleExecutionDispatch_InvalidScript(t *testing.T) {
	avail := executor.Availability{Shell: executor.BackendStatus{Available: true}}
	runner := &fakeTaskRunner{tasks: make(map[string]*model.Task)}
	h := newDispatchInboundHandler(runner, nil, avail)

	err := h.HandleExecutionDispatch(protocol.ExecutionDispatchMessage{
		Execution: &protocol.Execution{
			ExecutionID: "exec-1",
			Script:      []byte(`{bad json`),
		},
	})
	require.Error(t, err)
}

func TestHandleExecutionDispatch_Success(t *testing.T) {
	avail := executor.Availability{Shell: executor.BackendStatus{Available: true}}
	runner := &fakeTaskRunner{tasks: make(map[string]*model.Task)}
	h := newDispatchInboundHandler(runner, nil, avail)

	script := shellScript(t, "echo hello")
	err := h.HandleExecutionDispatch(protocol.ExecutionDispatchMessage{
		Execution: &protocol.Execution{
			ExecutionID: "exec-abc",
			TaskID:      "my-task",
			Script:      script,
		},
	})
	require.NoError(t, err)
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
	err := h.HandleExecutionDispatch(protocol.ExecutionDispatchMessage{
		Execution: &protocol.Execution{
			ExecutionID: "exec-fail",
			TaskID:      "some-task",
			Script:      script,
		},
	})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindConflict, ce.Kind)
}

func TestHandleExecutionDispatch_TriggerError_WithRun(t *testing.T) {
	avail := executor.Availability{Shell: executor.BackendStatus{Available: true}}
	run := &sqlcdb.Run{ID: "r1", Status: sqlcdb.PhaseEnded}
	runner := &fakeTaskRunner{
		tasks:   make(map[string]*model.Task),
		trigErr: errors.New("conflict"),
		trigRun: run,
	}
	h := newDispatchInboundHandler(runner, nil, avail)

	script := shellScript(t, "echo hi")
	err := h.HandleExecutionDispatch(protocol.ExecutionDispatchMessage{
		Execution: &protocol.Execution{
			ExecutionID: "exec-conflict",
			TaskID:      "task",
			Script:      script,
		},
	})
	require.Error(t, err)
}

// --- HandleExecutionStop ---

func TestHandleExecutionStop_EmptyID(t *testing.T) {
	h := newTestInboundHandler()
	err := h.HandleExecutionStop(protocol.ExecutionStopMessage{ExecutionID: ""})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindValidation, ce.Kind)
}

func TestHandleExecutionStop_NotFound(t *testing.T) {
	repo := &stubRunRepo{getErr: ErrNotFound}
	runner := &fakeTaskRunner{}
	h := newDispatchInboundHandler(runner, repo, executor.Availability{})

	err := h.HandleExecutionStop(protocol.ExecutionStopMessage{ExecutionID: "exec-1"})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindConflict, ce.Kind)
}

func TestHandleExecutionStop_RunAlreadyTerminal(t *testing.T) {
	reason := sqlcdb.ReasonSuccess
	run := &sqlcdb.Run{Status: sqlcdb.PhaseEnded, EndReason: &reason}
	repo := &stubRunRepo{run: run}
	runner := &fakeTaskRunner{}
	h := newDispatchInboundHandler(runner, repo, executor.Availability{})

	err := h.HandleExecutionStop(protocol.ExecutionStopMessage{ExecutionID: "exec-1"})
	require.NoError(t, err)
}

func TestHandleExecutionStop_RunActive_NotRunning(t *testing.T) {
	run := &sqlcdb.Run{Status: sqlcdb.PhasePending}
	repo := &stubRunRepo{run: run}
	runner := &fakeTaskRunner{}
	h := newDispatchInboundHandler(runner, repo, executor.Availability{})

	err := h.HandleExecutionStop(protocol.ExecutionStopMessage{ExecutionID: "exec-1"})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindConflict, ce.Kind)
}

func TestHandleExecutionStop_RepoTransientError(t *testing.T) {
	repo := &stubRunRepo{getErr: errors.New("db error")}
	runner := &fakeTaskRunner{}
	h := newDispatchInboundHandler(runner, repo, executor.Availability{})

	err := h.HandleExecutionStop(protocol.ExecutionStopMessage{ExecutionID: "exec-1"})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindTransient, ce.Kind)
}

// --- HandleLogReplayRequest ---

func TestHandleLogReplayRequest_EmptyID(t *testing.T) {
	h := newTestInboundHandler()
	_, err := h.HandleLogReplayRequest(protocol.LogReplayRequestMessage{ExecutionID: ""})
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindValidation, ce.Kind)
}

func TestHandleLogReplayRequest_NotFound(t *testing.T) {
	repo := &stubRunRepo{getErr: ErrNotFound}
	h := newDispatchInboundHandler(nil, repo, executor.Availability{})

	chunk, err := h.HandleLogReplayRequest(protocol.LogReplayRequestMessage{
		ID:          "req-1",
		ExecutionID: "exec-1",
	})
	require.NoError(t, err)
	assert.True(t, chunk.Final)
}

func TestHandleLogReplayRequest_TransientError(t *testing.T) {
	repo := &stubRunRepo{getErr: errors.New("db timeout")}
	h := newDispatchInboundHandler(nil, repo, executor.Availability{})

	_, err := h.HandleLogReplayRequest(protocol.LogReplayRequestMessage{
		ID:          "req-1",
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
	assert.False(t, h.RemoveLogListener("exec-1"), "removing absent listener must return false")
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
	assert.True(t, h.RemoveLogListener("exec-1"))
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
