// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime/services"
)

// DefaultConcurrencyLimit is used when a task does not configure max_concurrent.
const DefaultConcurrencyLimit = 1

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
	// RestartAttempt carries the non-service restart-chain depth from the
	// triggering options so retireRun can escalate the restart backoff. Zero for
	// original, cron, API, retry, and queued runs. Read only under the manager
	// lock; not persisted.
	RestartAttempt int
}

// taskState holds all per-task runtime state under the manager mutex.
type taskState struct {
	task   *model.Task
	active []*ActiveRun

	// queue is populated only when task.OnOverlap == PolicyQueue.
	queue []*model.Run
	// cond signals the queue-drain goroutine. Allocated alongside queue.
	cond *sync.Cond

	// supervisor tracks instance slots and restart-attempt counters; non-nil
	// only when task.Kind.IsService().
	supervisor *services.Supervisor

	// removed latches when a reload drops this task. It stops the queue-drain
	// goroutine and tells recordRunOutcome to delete the taskState once the last
	// in-flight run retires. Guarded by m.mu like every other taskState field.
	// Cleared when UpsertTask revives a task a prior reload had removed.
	removed bool

	// queueDraining reports whether a queueProcessLoop goroutine is currently
	// alive for this state. Set true when the loop is spawned, cleared when it
	// exits (both under m.mu). UpsertTask uses it to re-arm the drain after a
	// remove+re-add cycle left cond non-nil but the goroutine gone — gating the
	// spawn on cond==nil alone would silently leave a revived queue task with no
	// drain. Guarded by m.mu.
	queueDraining bool
}

// concurrencyAction describes what the caller should do after evaluating a
// concurrency policy.
type concurrencyAction int

const (
	actionStart     concurrencyAction = iota // start the run immediately
	actionQueued                             // run was enqueued; do not start
	actionRejected                           // run was rejected (policy: skip)
	actionQueueFull                          // queue at queue_max; drop new firing
)

// evaluateConcurrency decides whether a run can start and mutates queue state
// accordingly. Must be called with m.mu held.
func (m *defaultTaskManager) evaluateConcurrency(ts *taskState, run *model.Run, concurrencyLimit int) (concurrencyAction, error) {
	if len(ts.active) < concurrencyLimit {
		return actionStart, nil
	}

	switch ts.task.OnOverlap {
	case model.PolicySkip:
		return actionRejected, fmt.Errorf("task already running, skipping (policy: skip)")
	case model.PolicyQueue:
		queueMax := ts.task.QueueMax
		if queueMax > 0 && len(ts.queue) >= queueMax {
			return actionQueueFull, fmt.Errorf("queue full (%d pending) for task %s", queueMax, ts.task.Name)
		}
		ts.queue = append(ts.queue, run)
		ts.cond.Signal()
		slog.Debug("Task queued", "name", ts.task.Name, "active", len(ts.active), "limit", concurrencyLimit, "queue", len(ts.queue))
		return actionQueued, nil
	case model.PolicyTerminate:
		if len(ts.active) > 0 {
			oldest := ts.active[0]
			slog.Info("Terminating oldest run", "run", oldest.Run.ID, "task", ts.task.Name)
			oldest.Cancel()
		}
		return actionStart, nil
	default:
		return actionStart, nil
	}
}

// queueProcessLoop drains the per-task queue, starting runs as slots open.
// Holds m.mu for its entire lifetime, releasing it only via cond.Wait.
func (m *defaultTaskManager) queueProcessLoop(taskName string) {
	defer m.wg.Done()
	m.mu.Lock()
	defer m.mu.Unlock()

	ts := m.tasks[taskName]
	if ts == nil {
		// A reload removed the task before this loop acquired the lock: the
		// taskState was deleted (it had nothing in flight), so there is nothing
		// to drain. Exit rather than dereference a nil state.
		return
	}
	// Clear the alive flag on exit (under m.mu) so a later UpsertTask revive can
	// tell the drain is gone and spawn a fresh loop.
	defer func() { ts.queueDraining = false }()
	for {
		for len(ts.queue) == 0 || len(ts.active) >= m.getConcurrencyLimit(ts.task) {
			if m.isShutdown.Load() || ts.removed {
				return
			}
			ts.cond.Wait()
		}
		// Re-check after the inner loop: Shutdown's (or RemoveTask's)
		// cond.Broadcast can wake us with a free slot and a non-empty queue.
		// Starting a run here would happen after the single cancel pass, leaving
		// its context live forever — an orphaned process and a drain that never
		// completes. The check is race-free under m.mu: the flag is set before
		// the lock is taken for the cancel+broadcast pass.
		if m.isShutdown.Load() || ts.removed {
			return
		}
		run := ts.queue[0]
		ts.queue = ts.queue[1:]
		m.startRun(ts.task, run, 0)
	}
}

// getConcurrencyLimit returns the configured max_concurrent, defaulting to 1.
func (m *defaultTaskManager) getConcurrencyLimit(task *model.Task) int {
	if task.MaxConcurrent == 0 {
		return DefaultConcurrencyLimit
	}
	return task.MaxConcurrent
}
