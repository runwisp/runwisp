// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"sync"
	"time"

	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/model"
)

type activeExecution struct {
	StartedAt *time.Time
}

// ExecutionTracker manages active cloud executions and buffers pending
// status updates that could not be sent while disconnected.
type ExecutionTracker struct {
	mu                      sync.Mutex
	activeExecutions        map[string]activeExecution
	pendingExecutionUpdates []protocol.ExecutionUpdateMessage
}

func NewExecutionTracker() *ExecutionTracker {
	return &ExecutionTracker{
		activeExecutions: make(map[string]activeExecution),
	}
}

func (t *ExecutionTracker) TrackRunning(executionID string, startedAt *time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.activeExecutions[executionID] = activeExecution{StartedAt: startedAt}
}

func (t *ExecutionTracker) Remove(executionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.activeExecutions, executionID)
}

func (t *ExecutionTracker) HasActive() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.activeExecutions) > 0
}

func (t *ExecutionTracker) QueueUpdate(update protocol.ExecutionUpdateMessage, trySend func(any) error) {
	if trySend != nil {
		if err := trySend(update); err == nil {
			return
		}
	}
	t.bufferUpdate(update)
}

func (t *ExecutionTracker) bufferUpdate(update protocol.ExecutionUpdateMessage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.pendingExecutionUpdates) >= maxPendingExecutionUpdates {
		copy(t.pendingExecutionUpdates, t.pendingExecutionUpdates[1:])
		t.pendingExecutionUpdates = t.pendingExecutionUpdates[:maxPendingExecutionUpdates-1]
	}
	t.pendingExecutionUpdates = append(t.pendingExecutionUpdates, update)
}

func (t *ExecutionTracker) FlushPending(send func(any) error) {
	t.mu.Lock()
	runningSnapshots := make([]protocol.ExecutionUpdateMessage, 0, len(t.activeExecutions))
	for executionID, active := range t.activeExecutions {
		runningSnapshots = append(runningSnapshots, NewExecutionUpdateMessage(executionID, protocol.ExecutionStatusRunning, nil, active.StartedAt, nil))
	}
	pending := append(t.pendingExecutionUpdates, runningSnapshots...)
	t.pendingExecutionUpdates = t.pendingExecutionUpdates[:0]
	t.mu.Unlock()

	for i, update := range pending {
		if err := send(update); err != nil {
			t.mu.Lock()
			t.pendingExecutionUpdates = append(t.pendingExecutionUpdates, pending[i:]...)
			t.mu.Unlock()
			return
		}
	}
}

var terminalReasonMap = map[model.EndReason]protocol.ExecutionStatus{
	model.ReasonSuccess:     protocol.ExecutionStatusOk,
	model.ReasonStopped:     protocol.ExecutionStatusStopped,
	model.ReasonTimeout:     protocol.ExecutionStatusTimeout,
	model.ReasonFailed:      protocol.ExecutionStatusErr,
	model.ReasonCrashed:     protocol.ExecutionStatusErr,
	model.ReasonLogOverflow: protocol.ExecutionStatusErr,
	model.ReasonStartFailed: protocol.ExecutionStatusErr,
}

func mapRunToExecutionUpdate(run *model.Run) *protocol.ExecutionUpdateMessage {
	if run == nil || run.ExternalExecutionID == nil {
		return nil
	}

	executionID := *run.ExternalExecutionID
	if run.Status == model.PhaseRunning {
		return ptr(NewExecutionUpdateMessage(executionID, protocol.ExecutionStatusRunning, nil, run.StartAt, nil))
	}

	if run.EndReason == nil {
		return nil
	}
	status, ok := terminalReasonMap[*run.EndReason]
	if !ok {
		return nil
	}
	return ptr(NewExecutionUpdateMessage(executionID, status, ptr(run.ExitCode), run.StartAt, run.EndAt))
}

func ptr[T any](value T) *T {
	return &value
}

func nowPtr() *time.Time {
	now := time.Now().UTC()
	return &now
}
