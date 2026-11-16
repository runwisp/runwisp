// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"errors"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/executor"
	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestHandler is a handler over a fake runner and an in-memory run store
// whose stored updates land in *updates.
func newTestHandler(t *testing.T) (*InboundHandler, *fakeTaskRunner, *memRuns, *[]*protocol.ExecutionUpdate) {
	t.Helper()
	runner := &fakeTaskRunner{tasks: map[string]*model.Task{}}
	runs := &memRuns{}
	var updates []*protocol.ExecutionUpdate
	h := NewInboundHandler(InboundHandlerDeps{
		TaskManager:  runner,
		RunRepo:      runs,
		LogDir:       t.TempDir(),
		Availability: executor.Availability{Shell: executor.BackendStatus{Available: true}},
		AppendUpdate: func(u *protocol.ExecutionUpdate) { updates = append(updates, u) },
		Uploader:     NewLogUploader(nil, runs, t.TempDir(), time.Now, nil),
		Tracker:      NewExecutionTracker(),
	})
	return h, runner, runs, &updates
}

func shellDispatch(id string) protocol.ExecutionDispatch {
	return protocol.ExecutionDispatch{ExecutionID: id, Task: &protocol.TaskRef{ID: "t"}, Spec: []byte(`{"type":"shell","script":"true"}`)}
}

func TestDispatch_FinishedDuplicateStoresItsUpdateAgain(t *testing.T) {
	h, runner, runs, updates := newTestHandler(t)
	runs.put(finishedRun("e1", "station-t"))

	require.NoError(t, h.HandleExecutionDispatch(t.Context(), shellDispatch("e1")))
	assert.Empty(t, runner.triggeredIDs(), "a known execution never runs twice")
	require.Len(t, *updates, 1)
	assert.Equal(t, protocol.ExecutionStatusFinished, (*updates)[0].Status)
	assert.True(t, h.tracker.Reserve("e1"), "the duplicate holds no reservation")
}

func TestDispatch_TriggerErrors(t *testing.T) {
	h, runner, _, _ := newTestHandler(t)

	// The run exists and already ended (skipped): accepted; its finished
	// update says why.
	runner.trigRun, runner.trigErr = &model.Run{ID: "r1"}, errors.New("skipped: overlap")
	require.NoError(t, h.HandleExecutionDispatch(t.Context(), shellDispatch("e1")))

	// No run at all: refused, and the id can be dispatched again.
	runner.trigRun, runner.trigErr = nil, errors.New("task is disabled")
	err := h.HandleExecutionDispatch(t.Context(), shellDispatch("e2"))
	se, ok := errors.AsType[*StationError](err)
	require.True(t, ok)
	assert.Equal(t, protocol.ErrorCodeConflict, se.Code)
	assert.True(t, h.tracker.Reserve("e2"))
}

func TestDispatch_UnknownExecTypeIsUnsupported(t *testing.T) {
	h, _, _, _ := newTestHandler(t)
	d := shellDispatch("e1")
	d.Spec = []byte(`{"type":"teleport"}`)
	err := h.HandleExecutionDispatch(t.Context(), d)
	se, ok := errors.AsType[*StationError](err)
	require.True(t, ok)
	assert.Equal(t, protocol.ErrorCodeUnsupported, se.Code)
}

func TestStop(t *testing.T) {
	h, _, runs, _ := newTestHandler(t)

	err := h.HandleExecutionStop(t.Context(), protocol.ExecutionStop{ExecutionID: "nope"})
	se, ok := errors.AsType[*StationError](err)
	require.True(t, ok)
	assert.Equal(t, protocol.ErrorCodeNotFound, se.Code)

	runs.put(finishedRun("done", "t"))
	assert.NoError(t, h.HandleExecutionStop(t.Context(), protocol.ExecutionStop{ExecutionID: "done"}), "stopping a finished execution already holds")

	_, err = requireExecutionID("  ")
	require.Error(t, err)
}
