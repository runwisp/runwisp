// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"errors"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
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
	run := &sqlcdb.Run{Status: sqlcdb.PhaseRunning}
	assert.Nil(t, mapRunToExecutionUpdate(run))
}

func TestMapRunToExecutionUpdateRunning(t *testing.T) {
	execID := "ext-123"
	now := time.Now()
	run := &sqlcdb.Run{
		Status:              sqlcdb.PhaseRunning,
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
	reason := sqlcdb.ReasonSuccess
	now := time.Now()
	run := &sqlcdb.Run{
		Status:              sqlcdb.PhaseEnded,
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

func TestMapRunToExecutionUpdateEndedNilReason(t *testing.T) {
	execID := "ext-789"
	run := &sqlcdb.Run{
		Status:              sqlcdb.PhaseEnded,
		ExternalExecutionID: &execID,
		EndReason:           nil,
	}
	assert.Nil(t, mapRunToExecutionUpdate(run))
}
