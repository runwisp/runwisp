// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/runwisp/runwisp/internal/crashguard"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime/services"
)

// ActiveRun holds context for an in-flight run.
type ActiveRun struct {
	Run    *model.Run
	Cancel context.CancelFunc
	// ForceKill, when non-nil, immediately SIGKILLs the underlying process.
	// Set by the executor after the backend has started; nil for backends
	// without an OS-level process (e.g. HTTP). Used by the daemon shutdown
	// coordinator to bound total shutdown time.
	ForceKill func()
	StartedAt time.Time
	// cancelled latches once this run has been asked to stop (currently only the
	// kill overlap policy). It stops a later trigger from re-cancelling an
	// already-dying run — which would leave the live run count growing past
	// max_concurrent while the same victim drains. Guarded by m.mu.
	cancelled bool
}

// taskState holds all per-task runtime state under the manager mutex.
type taskState struct {
	task   *model.Task
	active []*ActiveRun

	// queue holds runs waiting for queueProcessLoop to call startRun once a
	// slot frees. Populated only when task.OnOverlap == PolicyQueue.
	queue []*model.Run
	// cond signals the queue-drain goroutine. Allocated alongside queue.
	cond *sync.Cond

	// supervisor tracks instance slots and restart-attempt counters; non-nil
	// only when task.Kind.IsService().
	supervisor *services.Supervisor

	// removed latches when RemoveTask evicts this task from the name-resolvable
	// registry (m.tasks) into removedTasks. It stops the queue-drain goroutine
	// and tells reapRetiredTaskState to delete it from removedTasks once the
	// last in-flight run retires. Guarded by m.mu like every other taskState
	// field. Cleared when UpsertTask revives a task a prior reload had removed.
	removed bool

	// queueDraining reports whether a queueProcessLoop goroutine is currently
	// alive for this state. Set true when the loop is spawned, cleared when it
	// exits (both under m.mu). UpsertTask uses it to re-arm the drain after a
	// remove+re-add cycle left cond non-nil but the goroutine gone — gating the
	// spawn on cond==nil alone would silently leave a revived queue task with no
	// drain. Guarded by m.mu.
	queueDraining bool

	// bookkeepingStop latches when the daemon, not the operator, stops a
	// service's supervisor: RemoveTask stopping it while it drains, or a reload
	// turning the service into a plain task. UpsertTask's revival branch clears
	// the stopped flag only when this is set: an operator's own StopService must
	// survive a reload, but a bookkeeping stop must not outlive the change it
	// was for, or a revived service comes back with zero live instances and no
	// error anywhere. Cleared by StopService/RestartServiceInstances too, since
	// either makes the stop state operator-driven from then on.
	bookkeepingStop bool
}

// concurrencyAction describes what the caller should do after evaluating a
// concurrency policy.
type concurrencyAction int

const (
	actionStart     concurrencyAction = iota // start the run immediately
	actionQueued                             // run was enqueued; do not start
	actionRejected                           // run was rejected (policy: skip)
	actionQueueFull                          // queue at max_queued; drop new firing
)

// evaluateConcurrency decides whether a run can start and mutates queue state
// accordingly. Must be called with m.mu held.
func (m *defaultTaskManager) evaluateConcurrency(ts *taskState, run *model.Run, concurrencyLimit int) (concurrencyAction, error) {
	// A free slot starts immediately — except under queue policy with runs
	// already waiting: a fresh trigger must join the back of the queue rather
	// than race ahead of runs the drain loop hasn't picked up yet. Without this,
	// in the window after a slot frees but before queueProcessLoop re-acquires
	// the lock, a new trigger would start out of FIFO order.
	queuePending := ts.task.OnOverlap == model.PolicyQueue && len(ts.queue) > 0
	if len(ts.active) < concurrencyLimit && !queuePending {
		return actionStart, nil
	}

	switch ts.task.OnOverlap {
	case model.PolicySkip:
		return actionRejected, fmt.Errorf("task already running, skipping (policy: skip)")
	case model.PolicyQueue:
		if maxQueued := ts.task.MaxQueuedValue(); len(ts.queue) >= maxQueued {
			return actionQueueFull, fmt.Errorf("queue full (%d pending) for task %s", maxQueued, ts.task.Name)
		}
		ts.queue = append(ts.queue, run)
		ts.cond.Signal()
		slog.Debug("Task queued", "name", ts.task.Name, "active", len(ts.active), "limit", concurrencyLimit, "queue", len(ts.queue))
		return actionQueued, nil
	case model.PolicyKill:
		m.cancelExcessRuns(ts, concurrencyLimit)
		return actionStart, nil
	default:
		return actionStart, nil
	}
}

// cancelExcessRuns cancels the oldest not-yet-cancelled runs of ts until
// enough are draining that active returns to concurrencyLimit once they exit.
// Skipping already-cancelled runs bounds the live set: re-cancelling the same
// dying run on every trigger would let active grow without limit.
//
// The cancel budget counts only live (not-yet-cancelled) runs. Counting
// still-draining victims too would over-kill healthy runs when
// max_concurrent > 1 and triggers arrive faster than victims drain.
func (m *defaultTaskManager) cancelExcessRuns(ts *taskState, concurrencyLimit int) {
	live := 0
	for _, ar := range ts.active {
		if !ar.cancelled {
			live++
		}
	}
	needed := live - concurrencyLimit + 1
	for _, ar := range ts.active {
		if needed <= 0 {
			break
		}
		if ar.cancelled {
			continue
		}
		ar.cancelled = true
		slog.Info("Terminating run to make room", "run", ar.Run.ID, "task", ts.task.Name)
		ar.Cancel()
		needed--
	}
}

// queueProcessLoop drains the per-task queue, starting runs as slots open.
// Holds m.mu for its entire lifetime, releasing it only via cond.Wait. It takes
// the taskState rather than looking it up by name: a reload that removes the
// task before this goroutine first runs would otherwise make it exit without
// clearing queueDraining, and a later revive would never respawn the drain.
func (m *defaultTaskManager) queueProcessLoop(ts *taskState) {
	defer crashguard.Guard()
	defer m.wg.Done()
	m.mu.Lock()
	defer m.mu.Unlock()

	// Clear the alive flag on exit (under m.mu) so a later UpsertTask revive can
	// tell the drain is gone and spawn a fresh loop.
	defer func() { ts.queueDraining = false }()
	for {
		for len(ts.queue) == 0 || len(ts.active) >= ts.task.MaxConcurrentValue() {
			if m.isShutdown.Load() || ts.removed || ts.task.OnOverlap != model.PolicyQueue {
				return
			}
			ts.cond.Wait()
		}
		// Re-check after the inner loop: Shutdown's (or RemoveTask's)
		// cond.Broadcast can wake us with a free slot and a non-empty queue.
		// Starting a run here would happen after the single cancel pass, leaving
		// its context live forever — an orphaned process and a drain that never
		// completes. The check is race-free under m.mu: the flag is set before
		// the lock is taken for the cancel+broadcast pass. ts.task.OnOverlap is
		// read under the same lock UpsertTask holds when it swaps in a reloaded
		// definition, so a reload that flips the policy away from queue is
		// observed here too — otherwise this goroutine would idle forever,
		// woken by every retireRun signal but never draining anything.
		if m.isShutdown.Load() || ts.removed || ts.task.OnOverlap != model.PolicyQueue {
			return
		}
		queued := ts.queue[0]
		ts.queue = ts.queue[1:]
		m.startRun(ts.task, queued)
	}
}
