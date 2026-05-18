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
const (
	PersistenceChannelSize = 1024
	errTaskNotFoundFmt     = "task not found: %s"
	errTaskNotServiceFmt   = "task %s is not a service"
)

// TriggerRunOptions customise run creation for non-local invocations.
type TriggerRunOptions struct {
	TriggeredBy         model.TriggeredBy
	ExternalExecutionID string
	RetryAttempt        int
	RetryOfRunID        *string
	// InstanceIndex pins the run to a specific instance slot. Required for
	// supervisor-driven restarts of services; nil for cron/API/retry runs.
	InstanceIndex *int
}

// Compile-time check: *defaultTaskManager satisfies TaskManager.
var _ TaskManager = (*defaultTaskManager)(nil)

// defaultTaskManager coordinates run lifecycles and concurrency policies.
type defaultTaskManager struct {
	executor    executor.Executor
	tasks       map[string]*taskState
	persistence *PersistenceCoordinator
	eventBus    events.EventBus
	// clock is injected so tests can pin run timestamps deterministically and
	// so the manager honours the project-wide invariant that wall-clock reads
	// inside scheduling logic come through an injected source. Production
	// wiring passes time.Now.
	clock      func() time.Time
	mu         sync.RWMutex
	isShutdown atomic.Bool
	// deadlineExceeded latches when the daemon-wide shutdown deadline fires.
	// Set BEFORE survivors are force-killed so each goroutine, on resolving
	// its run outcome, sees the flag and records ReasonDaemonStopped instead
	// of the per-task outcome.
	deadlineExceeded atomic.Bool
	wg               sync.WaitGroup
}

// NewTaskManager constructs the default run-manager. clock must not be nil;
// production wires time.Now, tests inject a fake to keep run timestamps
// deterministic.
func NewTaskManager(exec executor.Executor, bus events.EventBus, clock func() time.Time) TaskManager {
	if clock == nil {
		clock = time.Now
	}
	return &defaultTaskManager{
		executor:    exec,
		tasks:       make(map[string]*taskState),
		persistence: NewPersistenceCoordinator(PersistenceChannelSize),
		eventBus:    bus,
		clock:       clock,
	}
}

// BindPersistenceHook wires persistence to both the manager and executor.
// Also wires the executor's process-started callback so the manager can
// reach each active run's ForceKill closure during shutdown.
func (m *defaultTaskManager) BindPersistenceHook(hook RunPersistenceHook) {
	m.persistence.BindHook(hook, m.executor)

	type onStartedSetter interface {
		SetOnProcessStarted(func(runID string, forceKill func()))
	}
	if setter, ok := m.executor.(onStartedSetter); ok {
		setter.SetOnProcessStarted(m.registerForceKill)
	}
}

// registerForceKill stores the executor's ForceKill closure on the matching
// ActiveRun so the daemon shutdown coordinator can reach it later. Called
// from the executor goroutine right after the backend starts the process.
func (m *defaultTaskManager) registerForceKill(runID string, forceKill func()) {
	if forceKill == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ts := range m.tasks {
		for _, ar := range ts.active {
			if ar.Run.ID == runID {
				ar.ForceKill = forceKill
				return
			}
		}
	}
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
			ts.supervisor = services.NewSupervisor(task.Name, task.Instances, task.BackoffResetAfter)
		} else {
			ts.supervisor.SetInstances(task.Instances)
			ts.supervisor.SetBackoffReset(task.BackoffResetAfter)
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
// Service instances are never resumed — the supervisor spawns fresh runs at
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
		m.resumePendingRun(ts, &r, &result)
	}
	return result
}

// resumePendingRun applies the per-run policy from LoadPendingRuns: services
// are marked failed (the supervisor spawns fresh instances on boot), queued
// tasks rejoin their queue (or fail when it is full), and concurrent tasks
// either restart immediately or fail when capacity is exhausted.
func (m *defaultTaskManager) resumePendingRun(ts *taskState, r *model.Run, result *PendingRunsResult) {
	if ts.task.Kind.IsService() {
		r.End(model.ReasonFailed, -1, m.clock())
		m.persistence.PersistExisting(r)
		result.Skipped++
		return
	}

	if ts.task.OnOverlap == model.PolicyQueue {
		m.requeuePendingRun(ts, r, result)
		return
	}
	m.restartOrFailPendingRun(ts, r, result)
}

func (m *defaultTaskManager) requeuePendingRun(ts *taskState, r *model.Run, result *PendingRunsResult) {
	queueMax := getQueueMax(ts.task)
	if queueMax > 0 && len(ts.queue) >= queueMax {
		r.End(model.ReasonQueueFull, -1, m.clock())
		m.persistence.PersistExisting(r)
		result.Failed++
		return
	}
	ts.queue = append(ts.queue, r)
	ts.cond.Signal()
	result.Queued++
}

func (m *defaultTaskManager) restartOrFailPendingRun(ts *taskState, r *model.Run, result *PendingRunsResult) {
	concurrencyLimit := m.getConcurrencyLimit(ts.task)
	if len(ts.active) < concurrencyLimit {
		m.startRun(ts.task, r)
		result.Resumed++
		return
	}
	r.End(model.ReasonFailed, -1, m.clock())
	m.persistence.PersistExisting(r)
	result.Failed++
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
		return nil, fmt.Errorf(errTaskNotFoundFmt, taskName)
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
		idx, err := ts.supervisor.Reserve(options.InstanceIndex)
		if err != nil {
			return nil, err
		}
		run := &model.Run{
			ID:                  ulid.Make().String(),
			ExternalExecutionID: externalExecutionID,
			TaskName:            taskName,
			Status:              model.PhasePending,
			TriggeredBy:         triggeredBy,
			CreatedAt:           m.clock(),
			RetryAttempt:        options.RetryAttempt,
			RetryOfRunID:        options.RetryOfRunID,
			InstanceIndex:       idx,
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
		CreatedAt:           m.clock(),
		RetryAttempt:        options.RetryAttempt,
		RetryOfRunID:        options.RetryOfRunID,
	}

	m.persistence.PersistNew(run)
	m.publishRun(events.EventRunCreated, run)

	concurrencyLimit := m.getConcurrencyLimit(ts.task)
	action, actionErr := m.evaluateConcurrency(ts, run, concurrencyLimit)

	switch action {
	case actionRejected:
		run.End(model.ReasonSkipped, -1, m.clock())
		m.persistence.PersistExisting(run)
		return run, actionErr
	case actionQueueFull:
		run.End(model.ReasonQueueFull, -1, m.clock())
		m.persistence.PersistExisting(run)
		m.publishRun(events.EventRunFailed, run)
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

// RecordSkippedFiring persists a run that the runtime suppressed before any
// executor work (e.g. a DST wall-clock duplicate). The run lives only as an
// audit row — no process is started, no streams open. The run is created and
// then immediately ended with the supplied reason.
func (m *defaultTaskManager) RecordSkippedFiring(taskName string, reason model.EndReason, triggeredBy model.TriggeredBy) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.tasks[taskName]; !exists {
		return fmt.Errorf(errTaskNotFoundFmt, taskName)
	}

	now := m.clock()
	run := &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    taskName,
		Status:      model.PhasePending,
		TriggeredBy: triggeredBy,
		CreatedAt:   now,
	}
	m.persistence.PersistNew(run)
	m.publishRun(events.EventRunCreated, run)
	run.End(reason, -1, now)
	m.persistence.PersistExisting(run)
	m.publishRun(events.EventRunFailed, run)
	return nil
}

// StartServiceInstances brings every instance of a service up to its desired
// count. Idempotent — already-running instances are left untouched.
func (m *defaultTaskManager) StartServiceInstances(taskName string) error {
	m.mu.RLock()
	ts, exists := m.tasks[taskName]
	if !exists {
		m.mu.RUnlock()
		return fmt.Errorf(errTaskNotFoundFmt, taskName)
	}
	if !ts.task.Kind.IsService() {
		m.mu.RUnlock()
		return fmt.Errorf(errTaskNotServiceFmt, taskName)
	}
	if ts.supervisor.IsStopped() {
		m.mu.RUnlock()
		return nil
	}
	missing := ts.supervisor.MissingSlots()
	m.mu.RUnlock()

	for _, idx := range missing {
		i := idx
		if _, err := m.TriggerRunWithOptions(taskName, TriggerRunOptions{
			TriggeredBy:   model.TriggeredByAPI,
			InstanceIndex: &i,
		}); err != nil {
			slog.Error("Failed to start service instance", "task", taskName, "instance", i, "err", err)
		}
	}
	return nil
}

// RestartServiceInstances brings a service back to its desired instance count.
// If the service was operator-stopped, the stop flag is cleared and missing
// slots are spawned. If the service is already running, every active instance
// is cancelled and the exit handler refills the freed slots via the supervisor.
func (m *defaultTaskManager) RestartServiceInstances(taskName string) error {
	m.mu.Lock()
	ts, exists := m.tasks[taskName]
	if !exists {
		m.mu.Unlock()
		return fmt.Errorf(errTaskNotFoundFmt, taskName)
	}
	if !ts.task.Kind.IsService() {
		m.mu.Unlock()
		return fmt.Errorf(errTaskNotServiceFmt, taskName)
	}
	wasStopped := ts.supervisor.IsStopped()
	ts.supervisor.MarkRunning()
	for _, ar := range ts.active {
		ar.Cancel()
	}
	m.mu.Unlock()

	if wasStopped {
		return m.StartServiceInstances(taskName)
	}
	return nil
}

// StopService marks the service as operator-stopped (in-memory only, cleared
// on daemon restart) and cancels every live instance. The exit handler honours
// the flag and stops refilling slots.
func (m *defaultTaskManager) StopService(taskName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	ts, exists := m.tasks[taskName]
	if !exists {
		return fmt.Errorf(errTaskNotFoundFmt, taskName)
	}
	if !ts.task.Kind.IsService() {
		return fmt.Errorf(errTaskNotServiceFmt, taskName)
	}
	ts.supervisor.MarkStopped()
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
		StartedAt: m.clock(),
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

	endTime := m.clock()
	outcome := resolveRunOutcome(result)
	if m.deadlineExceeded.Load() {
		// Daemon shutdown ran past its bound — the run was force-killed by
		// the shutdown coordinator. Override the per-task outcome so the
		// audit row reflects the reason the operator cares about.
		outcome.endReason = model.ReasonDaemonStopped
		outcome.eventType = events.EventRunFailed
	}
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
		nextRestartAttempt = ts.supervisor.RecordExit(run.InstanceIndex, runDuration)
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

// Shutdown terminates all active runs and waits for them to drain. Equivalent
// to ShutdownWithDeadline(0) — no upper bound, runs settle on their own
// per-task graceful_stop ladders.
func (m *defaultTaskManager) Shutdown() {
	m.ShutdownWithDeadline(0)
}

// ShutdownWithDeadline cancels every active run, waits up to deadline for
// goroutines to exit cleanly, and on timeout SIGKILLs survivors so the
// daemon can exit without leaving orphaned processes behind. Surviving runs
// are recorded with ReasonDaemonStopped via the deadlineExceeded flag.
// deadline <= 0 means "wait indefinitely" (matches old behaviour).
func (m *defaultTaskManager) ShutdownWithDeadline(deadline time.Duration) {
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

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	if deadline <= 0 {
		<-done
		m.persistence.Shutdown()
		return
	}

	timer := time.NewTimer(deadline)
	defer timer.Stop()

	select {
	case <-done:
		// All goroutines exited within the deadline.
	case <-timer.C:
		// Set the latch BEFORE force-killing so any goroutine that observes
		// its own outcome after the kill records ReasonDaemonStopped.
		m.deadlineExceeded.Store(true)
		m.forceKillSurvivors()
		<-done
	}

	m.persistence.Shutdown()
}

// forceKillSurvivors fires the ForceKill closure on every active run so the
// underlying processes die immediately, unblocking their executor.Wait
// goroutines. Must be called only after m.deadlineExceeded is set.
func (m *defaultTaskManager) forceKillSurvivors() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ts := range m.tasks {
		for _, ar := range ts.active {
			if ar.ForceKill == nil {
				continue
			}
			slog.Warn("Daemon shutdown deadline exceeded — force-killing run",
				"task", ts.task.Name, "run", ar.Run.ID,
			)
			ar.ForceKill()
		}
	}
}
