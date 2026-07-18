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

// serviceHealthPollInterval is how often WaitServiceHealthy re-checks a
// dependency's readiness. Readiness is time-based, so a tick this short keeps
// boot gating responsive without busy-looping.
const serviceHealthPollInterval = 250 * time.Millisecond

// errShuttingDown is returned by TriggerRunWithOptions once Shutdown has begun.
// Refusing to start a run under the lock is what makes shutdown race-free: a
// run that slips past Shutdown's single cancel pass would never have its
// context cancelled, orphaning its process and hanging the drain forever.
var errShuttingDown = errors.New("task manager is shutting down")

// TriggerRunOptions customise run creation for non-local invocations.
type TriggerRunOptions struct {
	TriggeredBy         model.TriggeredBy
	ExternalExecutionID string
	RetryAttempt        int
	RetryOfRunID        *string
	// InstanceIndex pins the run to a specific instance slot. Required for
	// supervisor-driven restarts of services; nil for cron/API/retry runs.
	InstanceIndex *int
	// Params carries operator-supplied per-execution parameter values from a
	// manual trigger surface (REST/UI/TUI/cloud). Nil on scheduled/automatic
	// firings, which resolve to the task's declared defaults. A per-key nil
	// pointer explicitly omits that parameter even when it declares a default;
	// an absent key falls back to the default. See model.ResolveParamValues.
	Params map[string]*string
	// ScheduledAt backdates a jittered run's CreatedAt to the cron tick it
	// belongs to, while the run still starts at m.clock() (tick + offset). The
	// StartAt − CreatedAt delta then honestly shows the jitter, the way
	// RecordMissedRun backdates CreatedAt to the missed tick. Zero means "use
	// the clock", which is every non-jittered path. Non-service only.
	ScheduledAt time.Time
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
	// shutdownCtx is cancelled the instant ShutdownWithDeadline begins —
	// before the run-cancel pass and the wg drain. Goroutines parked in
	// waitForDelay (retry, restart, jittered dispatch) select on it so they
	// abort promptly at shutdown start. Without it they would block the drain:
	// wg can't reach zero until the goroutine exits, the goroutine can't exit
	// until its wait unblocks, and waiting on persistence.Done() never unblocks
	// because persistence shuts down only after the drain — a circular wait.
	shutdownCtx    context.Context
	shutdownCancel context.CancelFunc
	// gate is the daemon-wide work-conserving jitter gate. Jittered cron fires
	// are submitted to it instead of started directly; it targets one in-flight
	// jittered run at a time, pulling the next forward as soon as the box is
	// idle and breaching held tasks at their slots under congestion.
	gate *jitterGate
}

// NewTaskManager constructs the default run-manager. clock must not be nil;
// production wires time.Now, tests inject a fake to keep run timestamps
// deterministic.
func NewTaskManager(exec executor.Executor, bus events.EventBus, clock func() time.Time) TaskManager {
	if clock == nil {
		clock = time.Now
	}
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	m := &defaultTaskManager{
		executor:       exec,
		tasks:          make(map[string]*taskState),
		persistence:    NewPersistenceCoordinator(PersistenceChannelSize),
		eventBus:       bus,
		clock:          clock,
		shutdownCtx:    shutdownCtx,
		shutdownCancel: shutdownCancel,
	}
	m.gate = newJitterGate(clock, m.triggerJittered)
	return m
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
	} else {
		// Reviving a task a prior reload had removed while a run was still
		// draining: clear the stale removed latch so the old run's retireRun
		// won't delete the now-live task, and so the queue re-arms below. The
		// flag is reset nowhere else.
		ts.removed = false
	}
	ts.task = &taskCopy

	if task.Kind.IsService() {
		if ts.supervisor == nil {
			ts.supervisor = services.NewSupervisor(task.Name, task.Instances, task.HealthyAfter, !task.Autostart, m.clock)
		} else {
			ts.supervisor.SetInstances(task.Instances)
			ts.supervisor.SetHealthyAfter(task.HealthyAfter)
		}
	}

	if task.OnOverlap == model.PolicyQueue {
		if ts.queue == nil {
			ts.queue = make([]*model.Run, 0)
		}
		if ts.cond == nil {
			ts.cond = sync.NewCond(&m.mu)
		}
		// Spawn the drain loop only if one isn't already running. A remove+re-add
		// cycle leaves cond non-nil but the goroutine exited, so gate on the
		// liveness flag rather than cond == nil (which would leave the revived
		// queue task with no drain).
		if !ts.queueDraining {
			ts.queueDraining = true
			m.wg.Add(1)
			go m.queueProcessLoop(task.Name)
		}
	}
}

// RemoveTask drops a task from the manager when a reload removes it.
//
// The queue-drain goroutine (if any) is woken and exits via the removed flag.
// For services, every live instance is cancelled and the supervisor is marked
// stopped so the exit handler does not refill the slots. Cron tasks keep their
// in-flight runs — those finish under the definition they captured. The
// taskState is deleted now when nothing is in flight; otherwise the last run's
// recordRunOutcome deletes it on retirement (single-writer-per-task preserved).
func (m *defaultTaskManager) RemoveTask(taskName string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	ts, exists := m.tasks[taskName]
	if !exists {
		return
	}
	ts.removed = true

	// Drop anything still queued and wake the drain goroutine so it returns.
	if ts.cond != nil {
		ts.queue = nil
		ts.cond.Broadcast()
	}

	// Services don't drain — cancel their instances and stop the supervisor so
	// the exit handler won't bring them back.
	if ts.task.Kind.IsService() && ts.supervisor != nil {
		ts.supervisor.MarkStopped()
		for _, ar := range ts.active {
			ar.Cancel()
		}
	}

	if len(ts.active) == 0 {
		delete(m.tasks, taskName)
	}
}

// ListServiceTasks returns copies of every registered service task. Used by the
// cloud integration to fold daemon-supervised services (notably cloud-declared
// ones, registered at runtime via service:apply and absent from the TOML
// snapshot) into the tasks.sync payload, so the cloud knows they are live here.
func (m *defaultTaskManager) ListServiceTasks() []*model.Task {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]*model.Task, 0, len(m.tasks))
	for _, ts := range m.tasks {
		if ts.task == nil || !ts.task.Kind.IsService() {
			continue
		}
		taskCopy := *ts.task
		out = append(out, &taskCopy)
	}
	return out
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
	queueMax := ts.task.QueueMax
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

	// Refuse new runs once shutdown has begun. Shutdown sets isShutdown before
	// taking m.mu for its cancel pass, so checking it here under the lock is
	// race-free: a trigger either commits before Shutdown's pass (and gets
	// cancelled by it) or observes the flag and bails. Without this, a restart
	// that races shutdown could append a run after the cancel pass, leaving its
	// context live forever — an orphaned process and a hung drain.
	if m.isShutdown.Load() {
		return nil, errShuttingDown
	}

	ts, exists := m.tasks[taskName]
	if !exists {
		return nil, fmt.Errorf(errTaskNotFoundFmt, taskName)
	}

	triggeredBy := options.TriggeredBy
	if triggeredBy == "" {
		triggeredBy = model.TriggeredByService
	}

	var externalExecutionID *string
	if options.ExternalExecutionID != "" {
		externalIDCopy := options.ExternalExecutionID
		externalExecutionID = &externalIDCopy
	}

	// Resolve declared parameters against supplied values before any run row is
	// persisted, so a bad/unknown value rejects the trigger without leaving a
	// phantom run. Scheduled paths pass nil and get a defaults-only map. An empty
	// result collapses to nil so zero-param runs stay byte-identical.
	resolvedParams, err := model.ResolveParamValues(ts.task.Parameters, options.Params)
	if err != nil {
		return nil, err
	}
	if len(resolvedParams) == 0 {
		resolvedParams = nil
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
			Params:              resolvedParams,
		}
		m.persistence.PersistNew(run)
		m.publishRun(events.EventRunCreated, run)
		// Snapshot before startRun hands the live run to its execution goroutine.
		// The caller must never read fields (Status, StartAt, ...) that the run
		// goroutine concurrently writes, so it gets an independent copy.
		snapshot := run.Copy()
		m.startRun(ts.task, run)
		return snapshot, nil
	}

	// A jittered cron run records CreatedAt as the tick it belongs to (set by
	// the scheduler), even though it starts later at tick + offset. Every other
	// path leaves ScheduledAt zero and stamps the current clock.
	createdAt := m.clock()
	if !options.ScheduledAt.IsZero() {
		createdAt = options.ScheduledAt
	}

	run := &model.Run{
		ID:                  ulid.Make().String(),
		ExternalExecutionID: externalExecutionID,
		TaskName:            taskName,
		Status:              model.PhasePending,
		TriggeredBy:         triggeredBy,
		CreatedAt:           createdAt,
		RetryAttempt:        options.RetryAttempt,
		RetryOfRunID:        options.RetryOfRunID,
		Params:              resolvedParams,
	}

	m.persistence.PersistNew(run)
	m.publishRun(events.EventRunCreated, run)

	concurrencyLimit := m.getConcurrencyLimit(ts.task)
	action, actionErr := m.evaluateConcurrency(ts, run, concurrencyLimit)

	switch action {
	case actionRejected:
		run.End(model.ReasonSkipped, -1, m.clock())
		m.persistence.PersistExisting(run)
		m.publishRun(events.EventRunFailed, run)
		return run.Copy(), actionErr
	case actionQueueFull:
		run.End(model.ReasonQueueFull, -1, m.clock())
		m.persistence.PersistExisting(run)
		m.publishRun(events.EventRunFailed, run)
		return run.Copy(), actionErr
	case actionQueued:
		// The run sits in the queue; queueProcessLoop will later hand it to a
		// goroutine. Return a snapshot so the caller never races that promotion.
		return run.Copy(), nil
	case actionStart:
		// PolicyTerminate: evaluateConcurrency already cancelled the oldest
		// run. Do NOT eagerly remove it from active here — let the goroutine
		// clean up after executor.Execute returns, so the concurrency count
		// stays accurate.
	}

	// Snapshot before startRun spawns the execution goroutine (see service path).
	snapshot := run.Copy()
	m.startRun(ts.task, run)
	return snapshot, nil
}

// ScheduleJitteredRun submits a jittered cron fire to the work-conserving gate
// instead of starting it directly. tick is the cron tick (backdated onto the
// run's CreatedAt so the start delay surfaces as jitter, not hidden), slot the
// deadline — the latest the start may slip — and window the free-check horizon.
// The gate runs it at min(when it frees for this task, slot): pulled forward
// when the box is idle, released on its slot under congestion. No goroutine
// parks here; the gate arms a breach timer for held fires. If shutdown has begun
// the submit is dropped — the missed tick falls to catch-up on the next boot.
func (m *defaultTaskManager) ScheduleJitteredRun(taskName string, tick, slot time.Time, window time.Duration) {
	m.gate.submit(taskName, tick, slot, window)
}

// triggerJittered is the gate's run-start hook: it starts a jittered cron run,
// backdating CreatedAt to the tick, and reports whether the run was accepted
// (started or queued) so the gate knows to track it until completion. A refused
// trigger (shutdown) or a policy-rejected run is reported as not started and
// never tracked. Called by the gate under its own lock; TriggerRunWithOptions
// re-acquires the manager lock beneath it.
func (m *defaultTaskManager) triggerJittered(taskName string, tick time.Time) (string, bool) {
	run, err := m.TriggerRunWithOptions(taskName, TriggerRunOptions{
		TriggeredBy: model.TriggeredByCron,
		ScheduledAt: tick,
	})
	if err != nil {
		if !errors.Is(err, errShuttingDown) {
			slog.Error("Jittered run failed to start", "task", taskName, "err", err)
		}
		return "", false
	}
	return run.ID, true
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

// RecordMissedRun persists a terminal end_reason = "missed" run that documents
// a cron downtime gap, then publishes a failure-level event whose RunEvent.Error
// carries the human sentence built by the catch-up detector. Modeled on
// RecordSkippedFiring, with two deliberate differences: CreatedAt is the latest
// missed tick (scheduledAt) rather than now — so resolveCatchupAnchor reads it
// back as the last-alerted point and the next restart counts only ticks after
// it, never re-alerting — and the event carries the reason string verbatim so
// it renders as the notification body. No process is started, no log file
// exists; the run is created and immediately ended.
func (m *defaultTaskManager) RecordMissedRun(taskName string, scheduledAt time.Time, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.tasks[taskName]; !exists {
		return fmt.Errorf(errTaskNotFoundFmt, taskName)
	}

	run := &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    taskName,
		Status:      model.PhasePending,
		TriggeredBy: model.TriggeredByCron,
		CreatedAt:   scheduledAt,
	}
	m.persistence.PersistNew(run)
	m.publishRun(events.EventRunCreated, run)
	run.End(model.ReasonMissed, -1, scheduledAt)
	m.persistence.PersistExisting(run)
	m.publishRunErr(events.EventRunFailed, run, reason)
	return nil
}

// StartServiceInstances brings every instance of a service up to its desired
// count. Idempotent — already-running instances are left untouched. The
// triggeredBy argument labels the resulting runs: daemon boot passes
// TriggeredByService; an operator-initiated REST restart of a stopped service
// passes TriggeredByAPI.
func (m *defaultTaskManager) StartServiceInstances(taskName string, triggeredBy model.TriggeredBy) error {
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
			TriggeredBy:   triggeredBy,
			InstanceIndex: &i,
		}); err != nil {
			slog.Error("Failed to start service instance", "task", taskName, "instance", i, "err", err)
		}
	}
	return nil
}

// RestartServiceInstances brings a service back to its desired instance count.
// If the service was operator-stopped or had FATAL instances, the stop/FATAL
// flags are cleared and the empty slots are spawned with a fresh start-retry
// budget. If the service is already running, every active instance is cancelled
// and the exit handler refills the freed slots via the supervisor.
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
	// Capture FATAL state before MarkRunning clears it: a FATAL service has no
	// active runs in its dead slots, so they only come back if we spawn them.
	wasStopped := ts.supervisor.IsStopped()
	wasFatal := ts.supervisor.IsAnyFatal()
	ts.supervisor.MarkRunning()
	for _, ar := range ts.active {
		ar.Cancel()
	}
	m.mu.Unlock()

	if wasStopped || wasFatal {
		return m.StartServiceInstances(taskName, model.TriggeredByAPI)
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

// ServiceHealthy reports whether a service currently has at least one instance
// that has been running for at least its healthy_after. Non-services and
// unknown tasks report false. This is the live readiness signal consumed by
// depends_on boot gating.
func (m *defaultTaskManager) ServiceHealthy(taskName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ts, ok := m.tasks[taskName]
	if !ok || ts.supervisor == nil {
		return false
	}
	return ts.supervisor.IsHealthy()
}

// WaitServiceHealthy blocks until the named service is healthy, the context is
// cancelled, or the service can no longer reach healthy without operator
// intervention (operator-stopped, or every live slot gone and a FATAL one
// left). It returns nil only on healthy; every other exit is an error so the
// caller can decide whether to proceed anyway. It polls rather than waiting on
// a condition variable: readiness is time-based (healthy_after), so a slot
// already running crosses the threshold with no state change to signal on.
func (m *defaultTaskManager) WaitServiceHealthy(ctx context.Context, taskName string) error {
	if m.ServiceHealthy(taskName) {
		return nil
	}
	ticker := time.NewTicker(serviceHealthPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if m.ServiceHealthy(taskName) {
				return nil
			}
			if giveUp, err := m.serviceUnrecoverable(taskName); giveUp {
				return err
			}
		}
	}
}

// serviceUnrecoverable reports whether a service has no path back to healthy
// without operator action, so WaitServiceHealthy can stop polling early instead
// of burning the whole bounded window on a service that will never come up.
func (m *defaultTaskManager) serviceUnrecoverable(taskName string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ts, ok := m.tasks[taskName]
	if !ok {
		return true, fmt.Errorf(errTaskNotFoundFmt, taskName)
	}
	if ts.supervisor == nil {
		return true, fmt.Errorf(errTaskNotServiceFmt, taskName)
	}
	if ts.supervisor.IsStopped() {
		return true, fmt.Errorf("service %s is stopped", taskName)
	}
	if ts.supervisor.IsAnyFatal() && ts.supervisor.LiveCount() == 0 {
		return true, fmt.Errorf("service %s has no live instances and a slot is FATAL", taskName)
	}
	return false, nil
}

// ServiceSnapshot returns the supervisor + live-run view of a service task for
// reporting to cloud. ok is false when the task is unknown or not a service.
// Built under the manager lock so it is a consistent point-in-time.
func (m *defaultTaskManager) ServiceSnapshot(taskName string) (model.ServiceSnapshot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ts, exists := m.tasks[taskName]
	if !exists || ts.supervisor == nil || !ts.task.Kind.IsService() {
		return model.ServiceSnapshot{}, false
	}

	// Index live runs by instance slot to enrich slots with their start time.
	liveByIdx := make(map[int]*ActiveRun, len(ts.active))
	for _, ar := range ts.active {
		if ar.Run != nil {
			liveByIdx[ar.Run.InstanceIndex] = ar
		}
	}

	stopped := ts.supervisor.IsStopped()
	desired := ts.supervisor.Instances()
	instances := make([]model.ServiceInstanceStatus, 0, desired)
	running := 0
	fatal := 0
	for i := 0; i < desired; i++ {
		st := model.ServiceInstanceStatus{Index: i, RestartCount: ts.supervisor.Attempts(i)}
		switch {
		case ts.supervisor.IsLive(i):
			st.State = model.ServiceInstanceRunning
			running++
			if ar := liveByIdx[i]; ar != nil {
				started := ar.StartedAt
				st.StartedAt = &started
			}
		case stopped:
			st.State = model.ServiceInstanceStopped
		case ts.supervisor.IsFatal(i):
			// Slot exhausted its start-retry budget; the supervisor has given
			// up on it and won't respawn without operator intervention.
			st.State = model.ServiceInstanceFatal
			fatal++
		default:
			// Not live, not operator-stopped, not fatal: between exit and the
			// next backoff-delayed respawn.
			st.State = model.ServiceInstanceRestarting
		}
		instances = append(instances, st)
	}

	return model.ServiceSnapshot{
		TaskName:         taskName,
		State:            serviceRollupState(stopped, running, fatal, desired),
		DesiredInstances: desired,
		RunningInstances: running,
		Instances:        instances,
	}, true
}

func serviceRollupState(stopped bool, running, fatal, desired int) string {
	switch {
	case stopped:
		return model.ServiceStopped
	case running >= desired:
		return model.ServiceRunning
	case fatal >= desired:
		// Every slot has given up — the service is down and won't recover on
		// its own. Distinct from degraded, which is still trying to respawn.
		return model.ServiceFatal
	default:
		return model.ServiceDegraded
	}
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

// execute drives one run through its lifecycle: mark running, hand off to the
// executor, record the outcome, then schedule any policy-driven follow-up. The
// three phases are split out so each reads as one concern and can be tested
// without driving a full run.
func (m *defaultTaskManager) execute(ctx context.Context, task *model.Task, run *model.Run, active *ActiveRun) {
	run.Status = model.PhaseRunning
	run.StartAt = &active.StartedAt
	if task.Kind.IsService() {
		// Stamp the live-readiness clock the moment the instance is running so
		// dependents gating on this service measure uptime from here. Under the
		// manager lock since the supervisor is not internally synchronised.
		m.mu.Lock()
		if ts := m.tasks[task.Name]; ts != nil && ts.supervisor != nil {
			ts.supervisor.MarkLive(run.InstanceIndex)
		}
		m.mu.Unlock()
	}
	m.persistence.PersistExisting(run)
	m.publishRun(events.EventRunStarted, run)

	result := m.executor.Execute(ctx, task, run)

	nextRestartAttempt, serviceFatal := m.recordRunOutcome(task, run, active, result)
	// Advance the jitter gate now the run is retired from active and the
	// manager lock is released — recordRunOutcome unlocks before returning, so
	// the gate may re-enter TriggerRunWithOptions without deadlocking. A no-op
	// for runs the gate never triggered.
	m.gate.onComplete(run.ID)
	if !serviceFatal {
		m.scheduleFollowup(task, run, nextRestartAttempt)
	}
}

// recordRunOutcome classifies the executor result, ends and persists the run,
// publishes the terminal event, and retires the run from the task's active set
// (refreshing supervisor bookkeeping for services). It returns the next
// restart-attempt counter for the run's instance — meaningful only for
// services, zero otherwise — and whether the run's service instance is FATAL.
func (m *defaultTaskManager) recordRunOutcome(task *model.Task, run *model.Run, active *ActiveRun, result *executor.ExecuteResult) (int, bool) {
	endTime := m.clock()
	runDuration := endTime.Sub(active.StartedAt)
	outcome := runOutcome{
		endReason: result.EndReason(),
		eventType: events.EventRunCompleted,
	}
	if outcome.endReason != model.ReasonSuccess {
		outcome.eventType = events.EventRunFailed
	}
	if m.deadlineExceeded.Load() {
		// Daemon shutdown ran past its bound — the run was force-killed by
		// the shutdown coordinator. Override the per-task outcome so the
		// audit row reflects the reason the operator cares about. A
		// daemon-stopped exit is not a failure, so it never counts toward the
		// FATAL start-retry budget below.
		outcome.endReason = model.ReasonDaemonStopped
		outcome.eventType = events.EventRunFailed
	}

	// Supervisor bookkeeping happens before run.End so a FATAL transition can
	// rewrite the end reason: RecordExit needs runDuration, and whether the
	// instance has exhausted its start-retry budget decides the audit row.
	nextRestartAttempt, serviceFatal, fatalAttempts := m.retireRun(task, run, runDuration, outcome.endReason)

	if serviceFatal {
		outcome.endReason = model.ReasonStartFailed
		outcome.eventType = events.EventRunFailed
	}
	run.End(outcome.endReason, result.ExitCode, endTime)

	m.persistence.PersistExisting(run)
	m.publishRun(outcome.eventType, run)

	if serviceFatal {
		m.publishServiceFatal(task.Name, run.InstanceIndex, fatalAttempts, result.ExitCode)
		slog.Error("Service instance gave up: marked FATAL",
			"task", task.Name, "instance", run.InstanceIndex,
			"attempts", fatalAttempts, "exit_code", result.ExitCode)
	}

	return nextRestartAttempt, serviceFatal
}

// retireRun removes the run from its task's active set and updates supervisor
// bookkeeping under the manager lock. It returns the next restart-attempt
// counter (services only), whether the run's service instance is now FATAL, and
// the recorded start-fail count when FATAL (zero otherwise).
func (m *defaultTaskManager) retireRun(task *model.Task, run *model.Run, runDuration time.Duration, endReason model.EndReason) (nextRestartAttempt int, serviceFatal bool, fatalAttempts int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ts := m.tasks[task.Name]
	// ts can be nil only if the task was already deleted — RemoveTask never
	// deletes while a run is in flight, so in practice ts is present here; the
	// guard keeps a removed-then-resurrected edge from panicking.
	if ts == nil {
		return nextRestartAttempt, serviceFatal, fatalAttempts
	}
	for i, ar := range ts.active {
		if ar.Run.ID == run.ID {
			ts.active = append(ts.active[:i], ts.active[i+1:]...)
			break
		}
	}
	if task.Kind.IsService() {
		wasFailure := retry.IsFailureReason(endReason)
		nextRestartAttempt, serviceFatal = ts.supervisor.RecordExit(
			run.InstanceIndex, runDuration, task.StartRetries, wasFailure)
		if serviceFatal {
			fatalAttempts = ts.supervisor.StartFails(run.InstanceIndex)
		}
	}
	if ts.cond != nil {
		ts.cond.Signal()
	}
	// A reload removed this task while runs were draining; once the last one
	// retires the taskState has no further owner, so delete it here.
	if ts.removed && len(ts.active) == 0 {
		delete(m.tasks, task.Name)
	} else if ts.task != nil && ts.task.Ephemeral && len(ts.active) == 0 && len(ts.queue) == 0 {
		// Ephemeral cloud-inline tasks are one-shot and never enter the TOML
		// registry, so reconcile can't remove them. Reap here once the run
		// retires with nothing queued: mark removed and wake the queue-drain
		// goroutine so it exits, then drop the state. Holding m.mu makes the
		// "no active, no queued" check atomic w.r.t. queueProcessLoop.
		ts.removed = true
		if ts.cond != nil {
			ts.cond.Broadcast()
		}
		delete(m.tasks, task.Name)
	}
	return nextRestartAttempt, serviceFatal, fatalAttempts
}

// scheduleFollowup spawns the retry or restart goroutine dictated by the task's
// policy after a run has ended. Cloud-triggered runs never retry locally — the
// control plane owns their retry lifecycle.
// Service FATAL runs never reach this method — the caller guards on the FATAL
// flag returned by recordRunOutcome.
func (m *defaultTaskManager) scheduleFollowup(task *model.Task, run *model.Run, nextRestartAttempt int) {
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

// publishRunErr is publishRun with an error/reason string attached to the
// envelope. Used for runs that never executed but carry a human-readable
// explanation (e.g. a missed-run summary), surfaced as the notification body.
func (m *defaultTaskManager) publishRunErr(eventType events.EventType, run *model.Run, errMsg string) {
	if m.eventBus == nil {
		return
	}
	m.eventBus.Publish(eventType, events.RunEvent{
		Run:   run.Copy(),
		Error: errMsg,
	})
}

// publishServiceFatal announces that a service instance exhausted its
// start-retry budget and the supervisor gave up restarting it. notify maps
// this to a SevError in-app bell + global notifiers so the give-up is loud,
// not silent.
func (m *defaultTaskManager) publishServiceFatal(taskName string, instanceIndex, attempts, lastExitCode int) {
	if m.eventBus == nil {
		return
	}
	m.eventBus.Publish(events.EventServiceFatal, events.ServiceFatalEvent{
		TaskName:      taskName,
		InstanceIndex: instanceIndex,
		Attempts:      attempts,
		LastExitCode:  lastExitCode,
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

// GetActiveRunCount returns the number of active runs for the given task
// without allocating a slice. Unknown tasks return 0.
func (m *defaultTaskManager) GetActiveRunCount(taskName string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ts, exists := m.tasks[taskName]
	if !exists {
		return 0
	}
	return len(ts.active)
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
	// Cancel before the wg drain so any goroutine parked in waitForDelay exits
	// now instead of holding the drain open. Idempotent — safe if called twice.
	m.shutdownCancel()
	// Abandon pending jittered fires and stop their breach timers so no held
	// task starts a run after shutdown begins. Takes only the gate lock (no
	// manager lock held here), preserving the gateMu → mu order.
	m.gate.shutdown()
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
