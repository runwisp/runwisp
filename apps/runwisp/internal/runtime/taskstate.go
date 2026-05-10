// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
}

// taskState holds all per-task runtime state under the manager mutex.
type taskState struct {
	task   *model.Task
	active []*ActiveRun

	// queue is populated only when task.OnOverlap == PolicyQueue.
	queue []*model.Run
	// cond signals the queue-drain goroutine. Allocated alongside queue.
	cond *sync.Cond

	// supervisor tracks replica slots and restart-attempt counters; non-nil
	// only when task.Kind.IsService().
	supervisor *services.Supervisor
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
		queueMax := getQueueMax(ts.task)
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
	for {
		for len(ts.queue) == 0 || len(ts.active) >= m.getConcurrencyLimit(ts.task) {
			if m.isShutdown.Load() {
				return
			}
			ts.cond.Wait()
		}
		run := ts.queue[0]
		ts.queue = ts.queue[1:]
		m.startRun(ts.task, run)
	}
}

// getConcurrencyLimit returns the configured max_concurrent, defaulting to 1.
func (m *defaultTaskManager) getConcurrencyLimit(task *model.Task) int {
	if task.MaxConcurrent == 0 {
		return DefaultConcurrencyLimit
	}
	return task.MaxConcurrent
}

// getQueueMax returns the configured queue_max for a queue-policy task. The
// config layer applies a default; the zero return means "unbounded" only if
// the operator deliberately sets queue_max=0 (which the validator rejects).
func getQueueMax(task *model.Task) int {
	return task.QueueMax
}
