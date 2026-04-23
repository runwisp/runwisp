// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
	"log/slog"
)

const (
	DefaultConcurrencyLimit = 1
	// PersistenceChannelSize bounds the run-persistence work queue.
	// Sized for realistic burst (pending-run replay on startup) while staying
	// well below 1 MB of reserved channel ring memory.
	PersistenceChannelSize = 1024
)

// TriggerRunOptions customize run creation for non-local invocations.
type TriggerRunOptions struct {
	TriggeredBy         model.TriggeredBy
	ExternalExecutionID string
	RetryAttempt        int
	RetryOfRunID        *string
}

// concurrencyAction describes what the caller should do after evaluating a concurrency policy.
type concurrencyAction int

const (
	actionStart    concurrencyAction = iota // start the run immediately
	actionQueued                            // run was enqueued; do not start
	actionRejected                          // run was rejected (policy: skip)
)

// taskState holds all per-task runtime state under the manager mutex.
type taskState struct {
	task   *model.Task
	active []*ActiveRun
	queue  []*model.Run // non-nil only when task.Execution.Concurrency.Policy == PolicyQueue
	cond   *sync.Cond   // non-nil only when policy == PolicyQueue
}

// Compile-time check: *defaultTaskManager satisfies TaskManager.
var _ TaskManager = (*defaultTaskManager)(nil)

// defaultTaskManager coordinates run lifecycles and concurrency policies.
type defaultTaskManager struct {
	executor    executor.Executor
	tasks       map[string]*taskState
	persistence *PersistenceCoordinator
	eventBus    events.EventBus
	mu          sync.RWMutex
	isShutdown  int32 // atomic flag: 0 = running, 1 = shutdown
	wg          sync.WaitGroup
}

// ActiveRun holds context for an in-flight run.
type ActiveRun struct {
	Run       *model.Run
	Cancel    context.CancelFunc
	StartedAt time.Time
}

func NewTaskManager(exec executor.Executor, bus events.EventBus) TaskManager {
	return &defaultTaskManager{
		executor:    exec,
		tasks:       make(map[string]*taskState),
		persistence: NewPersistenceCoordinator(PersistenceChannelSize),
		eventBus:    bus,
	}
}

func (m *defaultTaskManager) publishRun(eventType events.EventType, run *model.Run, exitCode int) {
	if m.eventBus == nil {
		return
	}
	// Publish guarantees event ordering: run.created always arrives
	// before run.started for the same run. The SSE handler's buffered
	// channel provides the async decoupling.
	m.eventBus.Publish(eventType, events.RunEvent{
		Run: run.Copy(),
	})
}

// BindPersistenceHook wires persistence to both the manager and executor.
func (m *defaultTaskManager) BindPersistenceHook(hook RunPersistenceHook) {
	m.persistence.BindHook(hook, m.executor)
}

// UpsertTask adds a task if missing or replaces the existing definition.
func (m *defaultTaskManager) UpsertTask(task *model.Task) {
	m.mu.Lock()
	defer m.mu.Unlock()

	taskCopy := *task
	ts, exists := m.tasks[task.Name]
	if !exists {
		ts = &taskState{active: make([]*ActiveRun, 0)}
		m.tasks[task.Name] = ts
	}
	ts.task = &taskCopy

	if task.Execution.Concurrency.Policy == model.PolicyQueue && ts.cond == nil {
		ts.queue = make([]*model.Run, 0)
		ts.cond = sync.NewCond(&m.mu)
		m.wg.Add(1)
		go m.queueProcessLoop(task.Name)
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
			if atomic.LoadInt32(&m.isShutdown) == 1 {
				return
			}
			ts.cond.Wait()
		}
		run := ts.queue[0]
		ts.queue = ts.queue[1:]
		m.startRun(ts.task, run)
	}
}

// evaluateConcurrency decides whether a run can start and mutates queue state accordingly.
// Must be called with m.mu held.
func (m *defaultTaskManager) evaluateConcurrency(ts *taskState, run *model.Run, concurrencyLimit int) (concurrencyAction, error) {
	if len(ts.active) < concurrencyLimit {
		return actionStart, nil
	}

	switch ts.task.Execution.Concurrency.Policy {
	case model.PolicySkip:
		return actionRejected, fmt.Errorf("task already running, skipping (policy: skip)")
	case model.PolicyQueue:
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

// GetTask returns a copy of a registered task.
func (m *defaultTaskManager) GetTask(taskName string) (*model.Task, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ts, exists := m.tasks[taskName]
	if !exists {
		return nil, false
	}

	taskCopy := *ts.task
	return &taskCopy, true
}

// PendingRunsResult summarises what happened when resuming pending runs.
type PendingRunsResult struct {
	Resumed int
	Queued  int
	Skipped int
	Failed  int
}

// LoadPendingRuns re-queues runs that were pending when the system stopped.
func (m *defaultTaskManager) LoadPendingRuns(runs []model.Run) PendingRunsResult {
	m.mu.Lock()
	defer m.mu.Unlock()

	var result PendingRunsResult
	for _, run := range runs {
		r := run
		ts, exists := m.tasks[r.TaskName]
		if !exists {
			result.Skipped++
			continue
		}

		if ts.task.Execution.Concurrency.Policy == model.PolicyQueue {
			ts.queue = append(ts.queue, &r)
			ts.cond.Signal()
			result.Queued++
		} else {
			concurrencyLimit := m.getConcurrencyLimit(ts.task)
			if len(ts.active) < concurrencyLimit {
				m.startRun(ts.task, &r)
				result.Resumed++
			} else {
				r.End(model.ReasonFailed, -1, time.Now())
				m.persistence.PersistExisting(&r)
				result.Failed++
			}
		}
	}
	return result
}

func (m *defaultTaskManager) TriggerRun(taskName string, triggeredBy model.TriggeredBy) (*model.Run, error) {
	return m.TriggerRunWithOptions(taskName, TriggerRunOptions{
		TriggeredBy: triggeredBy,
	})
}

func (m *defaultTaskManager) TriggerRunWithOptions(taskName string, options TriggerRunOptions) (*model.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ts, exists := m.tasks[taskName]
	if !exists {
		return nil, fmt.Errorf("task not found: %s", taskName)
	}

	triggeredBy := options.TriggeredBy
	if triggeredBy == "" {
		triggeredBy = model.TriggeredByAPI
	}

	var externalExecutionID *string
	if options.ExternalExecutionID != "" {
		externalIDCopy := options.ExternalExecutionID
		externalExecutionID = &externalIDCopy
	}

	run := &model.Run{
		ID:                  ulid.Make().String(),
		ExternalExecutionID: externalExecutionID,
		TaskName:            taskName,
		Status:              model.PhasePending,
		TriggeredBy:         triggeredBy,
		CreatedAt:           time.Now(),
		RetryAttempt:        options.RetryAttempt,
		RetryOfRunID:        options.RetryOfRunID,
	}

	m.persistence.PersistNew(run)
	m.publishRun(events.EventRunCreated, run, 0)

	concurrencyLimit := m.getConcurrencyLimit(ts.task)
	action, actionErr := m.evaluateConcurrency(ts, run, concurrencyLimit)

	switch action {
	case actionRejected:
		run.End(model.ReasonFailed, -1, time.Now())
		m.persistence.PersistExisting(run)
		return run, actionErr
	case actionQueued:
		return run, nil
	case actionStart:
		// PolicyTerminate: evaluateConcurrency already cancelled the oldest run.
		// Do NOT eagerly remove it from active here — let the goroutine clean up
		// after executor.Execute returns, so the concurrency count stays accurate.
	}

	m.startRun(ts.task, run)
	return run, nil
}

// startRun registers the run and spawns the execution goroutine.
// Assumes m.mu is held.
func (m *defaultTaskManager) startRun(task *model.Task, run *model.Run) {
	ctx := context.Background()
	var cancel context.CancelFunc

	if task.Execution.Timeout != "" {
		if duration, err := time.ParseDuration(task.Execution.Timeout); err == nil {
			ctx, cancel = context.WithTimeout(ctx, duration)
		} else {
			slog.Warn("Invalid timeout", "task", task.Name, "err", err)
			ctx, cancel = context.WithCancel(ctx)
		}
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}

	active := &ActiveRun{
		Run:       run,
		Cancel:    cancel,
		StartedAt: time.Now(),
	}

	m.tasks[task.Name].active = append(m.tasks[task.Name].active, active)

	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.execute(ctx, task, run, active)
	}()
}

func (m *defaultTaskManager) execute(ctx context.Context, task *model.Task, run *model.Run, active *ActiveRun) {
	run.Status = model.PhaseRunning
	run.StartAt = &active.StartedAt
	m.persistence.PersistExisting(run)
	m.publishRun(events.EventRunStarted, run, 0)

	result := m.executor.Execute(ctx, task, run)

	endTime := time.Now()
	outcome := resolveRunOutcome(result)
	run.End(outcome.endReason, result.ExitCode, endTime)

	m.persistence.PersistExisting(run)
	m.publishRun(outcome.eventType, run, result.ExitCode)

	m.mu.Lock()
	ts := m.tasks[task.Name]
	for i, ar := range ts.active {
		if ar.Run.ID == run.ID {
			ts.active = append(ts.active[:i], ts.active[i+1:]...)
			break
		}
	}
	if ts.cond != nil {
		ts.cond.Signal()
	}
	m.mu.Unlock()

	// Retry logic: only for non-cloud runs (cloud retries are handled by the control plane)
	if run.TriggeredBy != model.TriggeredByCloud {
		copiedRun := run.Copy()
		switch {
		case shouldRestart(task, run):
			m.wg.Add(1)
			go func() {
				defer m.wg.Done()
				m.scheduleRestart(task, copiedRun)
			}()
		case shouldRetry(task, run):
			m.wg.Add(1)
			go func() {
				defer m.wg.Done()
				m.scheduleRetry(task, copiedRun)
			}()
		}
	}
}

type runOutcome struct {
	endReason model.EndReason
	eventType events.EventType
}

func resolveRunOutcome(result *executor.ExecuteResult) runOutcome {
	var reason model.EndReason
	switch {
	case result.TimedOut:
		reason = model.ReasonTimeout
	case result.Stopped:
		reason = model.ReasonStopped
	case result.ExitCode == 0:
		reason = model.ReasonSuccess
	default:
		reason = model.ReasonFailed
	}

	eventType := events.EventRunCompleted
	if reason != model.ReasonSuccess {
		eventType = events.EventRunFailed
	}

	return runOutcome{endReason: reason, eventType: eventType}
}

func (m *defaultTaskManager) getConcurrencyLimit(task *model.Task) int {
	if task.Execution.Concurrency.Limit == 0 {
		return DefaultConcurrencyLimit
	}
	return task.Execution.Concurrency.Limit
}

// GetActiveRuns returns a copy of active runs for the given task.
func (m *defaultTaskManager) GetActiveRuns(taskName string) []*ActiveRun {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ts, exists := m.tasks[taskName]
	if !exists {
		return nil
	}
	runs := make([]*ActiveRun, len(ts.active))
	copy(runs, ts.active)
	return runs
}

// TerminateRun cancels a running task by ID.
func (m *defaultTaskManager) TerminateRun(runID string) error {
	return m.cancelActiveRun(func(ar *ActiveRun) bool {
		return ar.Run.ID == runID
	}, fmt.Sprintf("run not found: %s", runID))
}

// TerminateRunByExternalExecutionID cancels a running run bound to an external execution ID.
func (m *defaultTaskManager) TerminateRunByExternalExecutionID(externalExecutionID string) error {
	return m.cancelActiveRun(func(ar *ActiveRun) bool {
		return ar.Run.ExternalExecutionID != nil && *ar.Run.ExternalExecutionID == externalExecutionID
	}, fmt.Sprintf("run not found for external execution id: %s", externalExecutionID))
}

func (m *defaultTaskManager) cancelActiveRun(match func(*ActiveRun) bool, notFoundMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, ts := range m.tasks {
		for _, ar := range ts.active {
			if match(ar) {
				ar.Cancel()
				return nil
			}
		}
	}

	return errors.New(notFoundMsg)
}

// Shutdown terminates all active runs and prepares for exit.
func (m *defaultTaskManager) Shutdown() {
	atomic.StoreInt32(&m.isShutdown, 1)
	m.mu.Lock()
	for _, ts := range m.tasks {
		for _, ar := range ts.active {
			ar.Cancel()
		}
		if ts.cond != nil {
			ts.cond.Broadcast()
		}
	}
	m.mu.Unlock()

	m.wg.Wait()
	m.persistence.Shutdown()
}
