// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime/retry"
	"github.com/runwisp/runwisp/internal/runtime/services"
)

// PersistenceChannelSize bounds the run-persistence work queue. Sized for
// realistic burst (pending-run replay on startup) while staying well below
// 1 MB of reserved channel ring memory.
const PersistenceChannelSize = 1024

// TriggerRunOptions customise run creation for non-local invocations.
type TriggerRunOptions struct {
	TriggeredBy         model.TriggeredBy
	ExternalExecutionID string
	RetryAttempt        int
	RetryOfRunID        *string
	// ReplicaIndex pins the run to a specific replica slot. Required for
	// supervisor-driven restarts of services; nil for cron/API/retry runs.
	ReplicaIndex *int
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
	isShutdown  atomic.Bool
	wg          sync.WaitGroup
}

func NewTaskManager(exec executor.Executor, bus events.EventBus) TaskManager {
	return &defaultTaskManager{
		executor:    exec,
		tasks:       make(map[string]*taskState),
		persistence: NewPersistenceCoordinator(PersistenceChannelSize),
		eventBus:    bus,
	}
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

	if task.Kind.IsService() {
		if ts.supervisor == nil {
			ts.supervisor = services.NewSupervisor(task.Name, task.Instances)
		} else {
			ts.supervisor.SetInstances(task.Instances)
		}
	}

	if task.OnOverlap == model.PolicyQueue && ts.cond == nil {
		ts.queue = make([]*model.Run, 0)
		ts.cond = sync.NewCond(&m.mu)
		m.wg.Add(1)
		go m.queueProcessLoop(task.Name)
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
// Service replicas are never resumed — the supervisor spawns fresh runs at
// the configured Instances count instead. Pending service rows are marked
// failed so they don't linger in the database.
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

		if ts.task.Kind.IsService() {
			r.End(model.ReasonFailed, -1, time.Now())
			m.persistence.PersistExisting(&r)
			result.Skipped++
			continue
		}

		if ts.task.OnOverlap == model.PolicyQueue {
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

	if ts.task.Kind.IsService() {
		idx, err := ts.supervisor.Reserve(options.ReplicaIndex)
		if err != nil {
			return nil, err
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
			ReplicaIndex:        idx,
		}
		m.persistence.PersistNew(run)
		m.publishRun(events.EventRunCreated, run)
		m.startRun(ts.task, run)
		return run, nil
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
	m.publishRun(events.EventRunCreated, run)

	concurrencyLimit := m.getConcurrencyLimit(ts.task)
	action, actionErr := m.evaluateConcurrency(ts, run, concurrencyLimit)

	switch action {
	case actionRejected:
		run.End(model.ReasonSkipped, -1, time.Now())
		m.persistence.PersistExisting(run)
		return run, actionErr
	case actionQueued:
		return run, nil
	case actionStart:
		// PolicyTerminate: evaluateConcurrency already cancelled the oldest
		// run. Do NOT eagerly remove it from active here — let the goroutine
		// clean up after executor.Execute returns, so the concurrency count
		// stays accurate.
	}

	m.startRun(ts.task, run)
	return run, nil
}

// StartServiceReplicas brings every replica of a service up to its desired
// count. Idempotent — already-running replicas are left untouched.
func (m *defaultTaskManager) StartServiceReplicas(taskName string) error {
	m.mu.RLock()
	ts, exists := m.tasks[taskName]
	if !exists {
		m.mu.RUnlock()
		return fmt.Errorf("task not found: %s", taskName)
	}
	if !ts.task.Kind.IsService() {
		m.mu.RUnlock()
		return fmt.Errorf("task %s is not a service", taskName)
	}
	missing := ts.supervisor.MissingSlots()
	m.mu.RUnlock()

	for _, idx := range missing {
		i := idx
		if _, err := m.TriggerRunWithOptions(taskName, TriggerRunOptions{
			TriggeredBy:  model.TriggeredByAPI,
			ReplicaIndex: &i,
		}); err != nil {
			slog.Error("Failed to start service replica", "task", taskName, "replica", i, "err", err)
		}
	}
	return nil
}

// RestartServiceReplicas cancels every active replica of a service. The exit
// handler refills each freed slot via the supervisor.
func (m *defaultTaskManager) RestartServiceReplicas(taskName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ts, exists := m.tasks[taskName]
	if !exists {
		return fmt.Errorf("task not found: %s", taskName)
	}
	if !ts.task.Kind.IsService() {
		return fmt.Errorf("task %s is not a service", taskName)
	}
	for _, ar := range ts.active {
		ar.Cancel()
	}
	return nil
}

// startRun registers the run and spawns the execution goroutine. Assumes
// m.mu is held.
func (m *defaultTaskManager) startRun(task *model.Task, run *model.Run) {
	ctx := context.Background()
	var cancel context.CancelFunc

	if task.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, task.Timeout)
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
	m.publishRun(events.EventRunStarted, run)

	result := m.executor.Execute(ctx, task, run)

	endTime := time.Now()
	outcome := resolveRunOutcome(result)
	run.End(outcome.endReason, result.ExitCode, endTime)

	m.persistence.PersistExisting(run)
	m.publishRun(outcome.eventType, run)

	runDuration := endTime.Sub(active.StartedAt)

	m.mu.Lock()
	ts := m.tasks[task.Name]
	for i, ar := range ts.active {
		if ar.Run.ID == run.ID {
			ts.active = append(ts.active[:i], ts.active[i+1:]...)
			break
		}
	}
	var nextRestartAttempt int
	if task.Kind.IsService() {
		nextRestartAttempt = ts.supervisor.RecordExit(run.ReplicaIndex, runDuration)
	}
	if ts.cond != nil {
		ts.cond.Signal()
	}
	m.mu.Unlock()

	// Retry logic: only for non-cloud runs (cloud retries are handled by the
	// control plane).
	if run.TriggeredBy == model.TriggeredByCloud {
		return
	}
	copiedRun := run.Copy()
	switch {
	case retry.ShouldRestart(task, run):
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			m.scheduleRestart(task, copiedRun, nextRestartAttempt)
		}()
	case retry.ShouldRetry(task, run):
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			m.scheduleRetry(task, copiedRun)
		}()
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
	case result.KilledByPolicy:
		// log_on_full = "kill_task" tripped: the run failed to stay inside
		// its log budget. Recorded as log_overflow so the cause is visible
		// at a glance; still treated as a failure for retry and notification
		// policy.
		reason = model.ReasonLogOverflow
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

func (m *defaultTaskManager) publishRun(eventType events.EventType, run *model.Run) {
	if m.eventBus == nil {
		return
	}
	// Publish guarantees event ordering: run.created always arrives before
	// run.started for the same run. The SSE handler's buffered channel
	// provides the async decoupling.
	m.eventBus.Publish(eventType, events.RunEvent{
		Run: run.Copy(),
	})
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

// TerminateRunByExternalExecutionID cancels a running run bound to an
// external execution ID.
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
	m.isShutdown.Store(true)
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
