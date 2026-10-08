// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

// PendingRunsResult summarises what happened when resuming pending runs.
type PendingRunsResult struct {
	Resumed int
	Queued  int
	Skipped int
	Failed  int
}

// LoadPendingRuns re-queues runs that were pending when the system stopped.
// Service instances are never resumed — the supervisor spawns fresh runs at
// the configured Instances count instead. Pending service rows are marked
// failed so they don't linger in the database.
func (m *defaultTaskManager) LoadPendingRuns(runs []model.Run) PendingRunsResult {
	// A pending run this pass ends without resuming gets its terminal event
	// published after m.mu is released, like every other path that ends a run
	// without executing it (see TriggerRunWithOptions).
	var endedRuns []*model.Run
	defer func() {
		for _, r := range endedRuns {
			m.publishTerminal(events.EventRunFailed, r, "")
		}
	}()
	m.mu.Lock()
	defer m.mu.Unlock()

	var result PendingRunsResult
	for _, r := range runs {
		ts, exists := m.tasks[r.TaskName]
		if !exists {
			m.endOrphanedPending(&r)
			endedRuns = append(endedRuns, &r)
			result.Skipped++
			continue
		}
		if ended := m.resumePendingRun(ts, &r, &result); ended != nil {
			endedRuns = append(endedRuns, ended)
		}
	}
	return result
}

// endOrphanedPending finalizes a still-pending run whose task no longer exists
// (removed by a reload, or absent from the config at boot) so it never lingers
// as a permanent non-terminal 'pending' row — retention only sweeps ended runs.
// Caller holds m.mu.
func (m *defaultTaskManager) endOrphanedPending(r *model.Run) {
	r.End(nil, model.ReasonSkipped, -1, m.clock())
	m.persistence.PersistExisting(r)
}

// resumePendingRun applies the per-run policy from LoadPendingRuns: services
// are marked failed (the supervisor spawns fresh instances on boot), queued
// tasks rejoin their queue (or fail when it is full), and concurrent tasks
// either restart immediately or fail when capacity is exhausted. It returns
// the run if it ended immediately (for the caller to publish once m.mu is
// released), or nil if it started or joined the queue instead.
func (m *defaultTaskManager) resumePendingRun(ts *taskState, r *model.Run, result *PendingRunsResult) *model.Run {
	if ts.task.Kind.IsService() {
		r.End(ts.task, model.ReasonFailed, -1, m.clock())
		m.persistence.PersistExisting(r)
		result.Skipped++
		return r
	}

	if ts.task.OnOverlap == model.PolicyQueue {
		return m.requeuePendingRun(ts, r, result)
	}
	return m.restartOrFailPendingRun(ts, r, result)
}

func (m *defaultTaskManager) requeuePendingRun(ts *taskState, r *model.Run, result *PendingRunsResult) *model.Run {
	if len(ts.queue) >= ts.task.MaxQueuedValue() {
		r.End(ts.task, model.ReasonQueueFull, -1, m.clock())
		m.persistence.PersistExisting(r)
		result.Failed++
		return r
	}
	ts.queue = append(ts.queue, r)
	ts.cond.Signal()
	result.Queued++
	return nil
}

func (m *defaultTaskManager) restartOrFailPendingRun(ts *taskState, r *model.Run, result *PendingRunsResult) *model.Run {
	concurrencyLimit := ts.task.MaxConcurrentValue()
	if len(ts.active) < concurrencyLimit {
		m.startRun(ts.task, r)
		result.Resumed++
		return nil
	}
	r.End(ts.task, model.ReasonFailed, -1, m.clock())
	m.persistence.PersistExisting(r)
	result.Failed++
	return r
}
