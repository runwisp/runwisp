// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"cmp"
	"context"
	"errors"
	"maps"
	"slices"
	"sync"

	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
)

// ExecutionTracker holds the station executions this daemon accepted and that
// haven't finished yet. Finished ones live on in the outbox until acked.
type ExecutionTracker struct {
	mu sync.Mutex
	// live maps each execution to accepted or running. accepted closes the
	// window between accepting a dispatch and the run reaching PhaseRunning:
	// without it a duplicate dispatch arriving in that window would start a
	// second run, because the run row is persisted asynchronously and carries
	// no unique constraint.
	live map[string]protocol.ExecutionStatus
}

func NewExecutionTracker() *ExecutionTracker {
	return &ExecutionTracker{live: map[string]protocol.ExecutionStatus{}}
}

// Reserve atomically claims executionID for a dispatch about to start. It
// returns false if the id is already reserved or running, i.e. a duplicate
// dispatch. Pair a successful Reserve with Release if the dispatch is
// rejected before a run exists; Remove frees it when the run finishes.
func (t *ExecutionTracker) Reserve(executionID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.live[executionID]; ok {
		return false
	}
	t.live[executionID] = protocol.ExecutionStatusAccepted
	return true
}

// Release drops a reservation whose dispatch never produced a run.
func (t *ExecutionTracker) Release(executionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.live[executionID] == protocol.ExecutionStatusAccepted {
		delete(t.live, executionID)
	}
}

func (t *ExecutionTracker) TrackRunning(executionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.live[executionID] = protocol.ExecutionStatusRunning
}

// Known reports whether executionID is accepted or running.
func (t *ExecutionTracker) Known(executionID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.live[executionID]
	return ok
}

// findExecutionRun returns the run of a station execution: the run dispatched
// under that id, else, when isService accepts it, the service instance's run
// with that run id (see executionIDOf). Neither is storage.ErrNotFound.
func findExecutionRun(ctx context.Context, runs ExternalRunGetter, id string, isService func(*model.Run) bool) (*model.Run, error) {
	run, err := runs.GetRunByExecutionID(ctx, id)
	if !errors.Is(err, storage.ErrNotFound) {
		return run, err
	}
	run, err = runs.GetRun(ctx, id)
	if err != nil {
		return nil, err
	}
	if run.ExecutionID != nil || !isService(run) {
		return nil, storage.ErrNotFound
	}
	return run, nil
}

func (t *ExecutionTracker) Remove(executionID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.live, executionID)
}

// Inventory lists every execution the daemon still owns for hello: accepted,
// running, or finished with an update the station hasn't acked (inOutbox).
// Never nil, so it marshals as a JSON array.
func (t *ExecutionTracker) Inventory(inOutbox []storage.OutboxExecution) []protocol.InventoryEntry {
	t.mu.Lock()
	byID := maps.Clone(t.live)
	t.mu.Unlock()
	for _, e := range inOutbox {
		if _, live := byID[e.ExecutionID]; !live {
			// The terminal update is stored before the run leaves the tracker,
			// so an outboxed execution that isn't live has finished.
			byID[e.ExecutionID] = protocol.ExecutionStatusFinished
		}
	}
	out := make([]protocol.InventoryEntry, 0, len(byID))
	for id, status := range byID {
		out = append(out, protocol.InventoryEntry{ExecutionID: id, Status: status})
	}
	slices.SortFunc(out, func(a, b protocol.InventoryEntry) int { return cmp.Compare(a.ExecutionID, b.ExecutionID) })
	return out
}

// executionIDOf is the id the station knows run by: its dispatch's execution
// id, or, for a service instance's run (which the daemon starts itself), the
// run's own id.
func executionIDOf(run *model.Run) string {
	if run.ExecutionID != nil {
		return *run.ExecutionID
	}
	return run.ID
}

// executionUpdate maps a run the station hears of (a dispatched one, or a
// service instance's) onto its execution:update (without seq, which the
// outbox assigns). A service instance's run also names its task and instance,
// so the station can record a run it never dispatched. It returns nil for a
// run that is neither running nor ended. reason is the run's EndReason as is;
// the station falls back to isFailure for a reason it doesn't know.
func executionUpdate(run *model.Run) *protocol.ExecutionUpdate {
	if run == nil {
		return nil
	}
	update := &protocol.ExecutionUpdate{ExecutionID: executionIDOf(run), StartedAt: unixMs(run.StartedAt)}
	if run.ExecutionID == nil {
		update.TaskID = run.TaskName
		update.Instance = new(run.InstanceIndex)
	}
	switch {
	case run.Status == model.PhaseRunning:
		update.Status = protocol.ExecutionStatusRunning
		return update
	case run.Status.IsTerminal() && run.EndReason != nil:
	default:
		return nil
	}
	update.Status = protocol.ExecutionStatusFinished
	update.FinishedAt = unixMs(run.EndedAt)
	update.Reason = string(*run.EndReason)
	update.IsFailure = new(run.IsFailure)
	update.ExitCode = new(run.ExitCode)
	if run.PeakMemoryBytes != nil || run.CPUTimeMs != nil {
		update.Usage = &protocol.ExecutionUsage{PeakMemoryBytes: run.PeakMemoryBytes, CpuTimeMs: run.CPUTimeMs}
	}
	return update
}
