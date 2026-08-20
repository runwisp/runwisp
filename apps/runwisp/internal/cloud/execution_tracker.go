// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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

// IsActive reports whether the given execution is currently tracked as
// running. Used by the dispatch idempotency guard to re-ack duplicate
// dispatches without starting a second run.
func (t *ExecutionTracker) IsActive(executionID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.activeExecutions[executionID]
	return ok
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
	// Copy into a fresh slice rather than appending onto pendingExecutionUpdates:
	// append may reuse that slice's backing array, and resetting the field to
	// [:0] keeps that array live, so a concurrent bufferUpdate would write into
	// slots this goroutine iterates over after the unlock. Set the source to nil
	// so the two slices share no storage.
	pending := make([]protocol.ExecutionUpdateMessage, 0, len(t.pendingExecutionUpdates)+len(runningSnapshots))
	pending = append(pending, t.pendingExecutionUpdates...)
	pending = append(pending, runningSnapshots...)
	t.pendingExecutionUpdates = nil
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

// terminalReasonMap maps every terminal EndReason to the wire status the cloud
// understands. It must stay exhaustive over model's EndReason set: a run only
// carries an EndReason once it has ended, so any reason reaching here is
// terminal and must yield a terminal update — otherwise the cloud tracks the
// execution as running forever. The protocol's status vocabulary is narrower
// than the daemon's reasons, so the never-executed/interrupted reasons collapse
// onto the closest fit: "skipped" for a run that never started because the
// concurrency/schedule policy said not to (never ran, not a failure), stopped
// for interruptions, and err for everything that did not complete successfully.
var terminalReasonMap = map[model.EndReason]protocol.ExecutionStatus{
	model.ReasonSuccess:       protocol.ExecutionStatusSucceeded,
	model.ReasonStopped:       protocol.ExecutionStatusStopped,
	model.ReasonTimeout:       protocol.ExecutionStatusTimeout,
	model.ReasonFailed:        protocol.ExecutionStatusFailed,
	model.ReasonCrashed:       protocol.ExecutionStatusFailed,
	model.ReasonLogOverflow:   protocol.ExecutionStatusFailed,
	model.ReasonStartFailed:   protocol.ExecutionStatusFailed,
	model.ReasonDaemonStopped: protocol.ExecutionStatusStopped,
	model.ReasonSkipped:       protocol.ExecutionStatusSkipped,
	model.ReasonQueueFull:     protocol.ExecutionStatusFailed,
	model.ReasonDSTSkipped:    protocol.ExecutionStatusSkipped,
	model.ReasonMissed:        protocol.ExecutionStatusSkipped,
}

func mapRunToExecutionUpdate(run *model.Run) *protocol.ExecutionUpdateMessage {
	if run == nil || run.ExecutionID == nil {
		return nil
	}

	executionID := *run.ExecutionID
	if run.Status == model.PhaseRunning {
		return ptr(NewExecutionUpdateMessage(executionID, protocol.ExecutionStatusRunning, nil, run.StartedAt, nil))
	}

	if run.EndReason == nil {
		return nil
	}
	status, ok := terminalReasonMap[*run.EndReason]
	if !ok {
		// Fail safe: a terminal reason not yet in the map (e.g. one added later)
		// must still report terminally rather than silently dropping the update
		// and stranding the execution as "running" on the cloud.
		status = protocol.ExecutionStatusFailed
	}
	return ptr(NewExecutionUpdateMessage(executionID, status, ptr(run.ExitCode), run.StartedAt, run.EndedAt))
}

func ptr[T any](value T) *T {
	return &value
}

func nowPtr() *time.Time {
	now := time.Now().UTC()
	return &now
}
