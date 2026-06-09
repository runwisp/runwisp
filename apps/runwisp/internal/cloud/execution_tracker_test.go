// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"errors"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTrackRunningAndRemove(t *testing.T) {
	tracker := NewExecutionTracker()
	now := time.Now()

	tracker.TrackRunning("exec-1", &now)
	assert.True(t, tracker.HasActive())

	tracker.Remove("exec-1")
	assert.False(t, tracker.HasActive())
}

func TestTrackMultipleExecutions(t *testing.T) {
	tracker := NewExecutionTracker()
	now := time.Now()

	tracker.TrackRunning("exec-a", &now)
	tracker.TrackRunning("exec-b", &now)
	assert.True(t, tracker.HasActive())

	tracker.Remove("exec-a")
	assert.True(t, tracker.HasActive())

	tracker.Remove("exec-b")
	assert.False(t, tracker.HasActive())
}

func TestQueueUpdateWithNilTrySend(t *testing.T) {
	tracker := NewExecutionTracker()
	update := NewExecutionUpdateMessage("exec-1", protocol.ExecutionStatusRunning, nil, nil, nil)

	tracker.QueueUpdate(update, nil)

	var delivered []any
	tracker.FlushPending(func(msg any) error {
		delivered = append(delivered, msg)
		return nil
	})
	require.Len(t, delivered, 1)
}

func TestQueueUpdateTrySendSuccess(t *testing.T) {
	tracker := NewExecutionTracker()
	update := NewExecutionUpdateMessage("exec-2", protocol.ExecutionStatusOk, nil, nil, nil)

	var sent []any
	tracker.QueueUpdate(update, func(msg any) error {
		sent = append(sent, msg)
		return nil
	})

	// Sent via trySend, nothing buffered.
	require.Len(t, sent, 1)
	var delivered []any
	tracker.FlushPending(func(msg any) error {
		delivered = append(delivered, msg)
		return nil
	})
	assert.Empty(t, delivered)
}

func TestQueueUpdateTrySendFailure(t *testing.T) {
	tracker := NewExecutionTracker()
	update := NewExecutionUpdateMessage("exec-3", protocol.ExecutionStatusErr, nil, nil, nil)

	tracker.QueueUpdate(update, func(any) error {
		return errors.New("send failed")
	})

	var delivered []any
	tracker.FlushPending(func(msg any) error {
		delivered = append(delivered, msg)
		return nil
	})
	require.Len(t, delivered, 1)
}

func TestMapRunToExecutionUpdateNilRun(t *testing.T) {
	assert.Nil(t, mapRunToExecutionUpdate(nil))
}

func TestMapRunToExecutionUpdateNilExternalExecutionID(t *testing.T) {
	run := &model.Run{Status: model.PhaseRunning}
	assert.Nil(t, mapRunToExecutionUpdate(run))
}

func TestMapRunToExecutionUpdateRunning(t *testing.T) {
	execID := "ext-123"
	now := time.Now()
	run := &model.Run{
		Status:              model.PhaseRunning,
		ExternalExecutionID: &execID,
		StartAt:             &now,
	}

	result := mapRunToExecutionUpdate(run)
	require.NotNil(t, result)
	assert.Equal(t, "execution:update", result.Type)
	assert.Equal(t, execID, result.ExecutionID)
	require.NotNil(t, result.Status)
	assert.Equal(t, protocol.ExecutionStatusRunning, *result.Status)
}

func TestMapRunToExecutionUpdateEndedSuccess(t *testing.T) {
	execID := "ext-456"
	reason := model.ReasonSuccess
	now := time.Now()
	run := &model.Run{
		Status:              model.PhaseEnded,
		ExternalExecutionID: &execID,
		EndReason:           &reason,
		ExitCode:            0,
		StartAt:             &now,
		EndAt:               &now,
	}

	result := mapRunToExecutionUpdate(run)
	require.NotNil(t, result)
	assert.Equal(t, execID, result.ExecutionID)
	require.NotNil(t, result.Status)
	assert.Equal(t, protocol.ExecutionStatusOk, *result.Status)
}

func TestMapRunToExecutionUpdateEndedStartFailed(t *testing.T) {
	execID := "ext-fatal"
	reason := model.ReasonStartFailed
	now := time.Now()
	run := &model.Run{
		Status:              model.PhaseEnded,
		ExternalExecutionID: &execID,
		EndReason:           &reason,
		ExitCode:            1,
		StartAt:             &now,
		EndAt:               &now,
	}

	// Without ReasonStartFailed in terminalReasonMap the control plane would
	// silently drop the give-up; it must surface as an error.
	result := mapRunToExecutionUpdate(run)
	require.NotNil(t, result)
	require.NotNil(t, result.Status)
	assert.Equal(t, protocol.ExecutionStatusErr, *result.Status)
}

func TestMapRunToExecutionUpdateEndedNilReason(t *testing.T) {
	execID := "ext-789"
	run := &model.Run{
		Status:              model.PhaseEnded,
		ExternalExecutionID: &execID,
		EndReason:           nil,
	}
	assert.Nil(t, mapRunToExecutionUpdate(run))
}

// FlushPending must include synthetic "running" snapshots for every currently
// tracked execution alongside the buffered updates.
func TestFlushPendingEmitsRunningSnapshotsForActiveExecutions(t *testing.T) {
	tracker := NewExecutionTracker()
	now := time.Now()
	tracker.TrackRunning("active-1", &now)

	// Buffer one terminal update.
	tracker.QueueUpdate(
		NewExecutionUpdateMessage("buffered-1", protocol.ExecutionStatusOk, nil, nil, nil),
		nil,
	)

	var delivered []protocol.ExecutionUpdateMessage
	tracker.FlushPending(func(msg any) error {
		if upd, ok := msg.(protocol.ExecutionUpdateMessage); ok {
			delivered = append(delivered, upd)
		}
		return nil
	})

	require.Len(t, delivered, 2, "must flush both buffered update and active snapshot")
	ids := []string{delivered[0].ExecutionID, delivered[1].ExecutionID}
	assert.Contains(t, ids, "buffered-1")
	assert.Contains(t, ids, "active-1")
}

// bufferUpdate drops the oldest entry when the buffer is at the cap so a
// long-disconnected daemon doesn't grow memory without bound.
func TestBufferUpdateDropsOldestAtCap(t *testing.T) {
	tracker := NewExecutionTracker()

	// Fill the buffer to its capacity by directly invoking bufferUpdate.
	for i := 0; i < maxPendingExecutionUpdates; i++ {
		tracker.bufferUpdate(NewExecutionUpdateMessage("first-batch", protocol.ExecutionStatusOk, nil, nil, nil))
	}
	tracker.mu.Lock()
	assert.Equal(t, maxPendingExecutionUpdates, len(tracker.pendingExecutionUpdates))
	tracker.mu.Unlock()

	// One more update — the oldest must be dropped, the newest appended.
	tracker.bufferUpdate(NewExecutionUpdateMessage("overflow", protocol.ExecutionStatusOk, nil, nil, nil))

	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	require.Equal(t, maxPendingExecutionUpdates, len(tracker.pendingExecutionUpdates),
		"buffer length must stay at the cap")
	assert.Equal(t, "overflow", tracker.pendingExecutionUpdates[maxPendingExecutionUpdates-1].ExecutionID,
		"newest update must be at the tail after the drop-oldest shift")
}

// FlushPending must re-queue any updates that haven't been delivered yet when
// send returns an error part-way through the slice.
func TestFlushPendingPartialFailureRequeuesRemainder(t *testing.T) {
	tracker := NewExecutionTracker()
	tracker.QueueUpdate(NewExecutionUpdateMessage("first", protocol.ExecutionStatusOk, nil, nil, nil), nil)
	tracker.QueueUpdate(NewExecutionUpdateMessage("second", protocol.ExecutionStatusOk, nil, nil, nil), nil)
	tracker.QueueUpdate(NewExecutionUpdateMessage("third", protocol.ExecutionStatusOk, nil, nil, nil), nil)

	delivered := 0
	tracker.FlushPending(func(any) error {
		delivered++
		if delivered == 2 {
			return errors.New("transient send failure")
		}
		return nil
	})

	// Second send failed → second and third must be re-buffered for the next attempt.
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	require.Len(t, tracker.pendingExecutionUpdates, 2)
	assert.Equal(t, "second", tracker.pendingExecutionUpdates[0].ExecutionID)
	assert.Equal(t, "third", tracker.pendingExecutionUpdates[1].ExecutionID)
}
