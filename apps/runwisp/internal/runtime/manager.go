// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/crashguard"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime/retry"
	"github.com/runwisp/runwisp/internal/runtime/services"
)

const (
	// persistenceChannelSize bounds the run-persistence work queue. Sized for
	// realistic burst (pending-run replay on startup) while staying well below
	// 1 MB of reserved channel ring memory.
	persistenceChannelSize = 1024
	errTaskNotFoundFmt     = "task not found: %s"
	errTaskNotServiceFmt   = "task %s is not a service"
	errTaskIsServiceFmt    = "task %s is a service, not a task"
)

// serviceHealthPollInterval is how often WaitServiceHealthy re-checks a
// dependency's readiness. Readiness is uptime- or probe-based, so a tick this
// short keeps boot gating responsive without busy-looping.
const serviceHealthPollInterval = 250 * time.Millisecond

// errShuttingDown is returned by TriggerRunWithOptions once Shutdown has begun.
// Refusing to start a run under the lock is what makes shutdown race-free: a
// run that slips past Shutdown's single cancel pass would never have its
// context cancelled, orphaning its process and hanging the drain forever.
var errShuttingDown = errors.New("task manager is shutting down")

// TriggerRunOptions customise run creation for non-local invocations.
type TriggerRunOptions struct {
	TriggeredBy  model.TriggeredBy
	ExecutionID  string
	RetryAttempt int
	RetryOfRunID *string
	// InstanceIndex pins the run to a specific instance slot. Required for
	// supervisor-driven restarts of services; nil for cron/API/retry runs.
	InstanceIndex *int
	// Params carries operator-supplied per-execution parameter values from a
	// manual trigger surface (REST/UI/TUI/station). Nil on scheduled/automatic
	// firings, which resolve to the task's declared defaults. A per-key nil
	// pointer explicitly omits that parameter even when it declares a default;
	// an absent key falls back to the default. See model.ResolveParamValues.
	Params map[string]*string
	// ScheduledAt backdates a jittered run's CreatedAt to the cron tick it
	// belongs to, while the run still starts at m.clock() (tick + offset). The
	// StartedAt − CreatedAt delta then honestly shows the jitter, the way
	// RecordMissedRun backdates CreatedAt to the missed tick. Zero means "use
	// the clock", which is every non-jittered path. Non-service only.
	ScheduledAt time.Time
}

// Compile-time check: *defaultTaskManager satisfies TaskManager.
var _ TaskManager = (*defaultTaskManager)(nil)

// defaultTaskManager coordinates run lifecycles and concurrency policies.
type defaultTaskManager struct {
	executor executor.Executor
	// tasks is the name-resolvable live registry. Every lookup-by-name path
	// (GetTask, TriggerRunWithOptions, the service-control methods, station's
	// dispatch/service handlers via those) reads only this map, so a task
	// dropped by RemoveTask becomes instantly unresolvable by name, and a station
	// peer (or REST/CLI restart) cannot race the drain window to resurrect a
	// task outside the current TOML set. See removedTasks.
	tasks map[string]*taskState
	// removedTasks holds taskStates RemoveTask evicted from tasks while a run
	// was still draining under its old definition — tracked here only so
	// internal bookkeeping that must reach every in-flight run regardless of
	// removal (retirement, force-kill, shutdown cancel/wait) still can, and so
	// UpsertTask can reattach the same object — active list and all — if the
	// same name is re-added before the drain finishes, preserving its
	// concurrency/instance accounting across the gap. Never consulted by a
	// name-based lookup. See allTaskStates and taskStateFor.
	removedTasks map[string]*taskState
	persistence  *PersistenceCoordinator
	eventBus     *events.Bus
	// clock is injected so wall-clock reads stay deterministic under test.
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
	// schedulePaused reports whether an operator paused the task's cron
	// schedule; set by NewScheduler (nil until then). Consulted only for a
	// jittered fire already waiting in the gate (see triggerJittered).
	schedulePaused func(string) bool
	// daemonLocation is the [daemon] timezone a health check cron with no
	// timezone of its own runs in; nil means time.Local. Set at boot in every
	// mode and on a reload that changes it (SetDaemonLocation).
	daemonLocation *time.Location
}

// setSchedulePaused wires the scheduler's pause check; see NewScheduler.
func (m *defaultTaskManager) setSchedulePaused(fn func(string) bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.schedulePaused = fn
}

// SetDaemonLocation sets the zone health check crons without their own
// timezone run in. Running health watchers read it on every tick, so a reload
// re-bases them without recycling the service.
func (m *defaultTaskManager) SetDaemonLocation(loc *time.Location) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.daemonLocation = loc
}

// location is the live daemon zone, falling back to time.Local.
func (m *defaultTaskManager) location() *time.Location {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return cmp.Or(m.daemonLocation, time.Local)
}

// NewTaskManager constructs the default run-manager. clock must not be nil;
// production wires time.Now, tests inject a fake to keep run timestamps
// deterministic.
func NewTaskManager(exec executor.Executor, bus *events.Bus, clock func() time.Time) TaskManager {
	if clock == nil {
		clock = time.Now
	}
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	m := &defaultTaskManager{
		executor:       exec,
		tasks:          make(map[string]*taskState),
		removedTasks:   make(map[string]*taskState),
		persistence:    NewPersistenceCoordinator(persistenceChannelSize),
		eventBus:       bus,
		clock:          clock,
		shutdownCtx:    shutdownCtx,
		shutdownCancel: shutdownCancel,
	}
	m.gate = newJitterGate(clock, m.triggerJittered)

	type runWatcherSetter interface {
		SetRunWatcher(executor.RunWatcher)
	}
	if setter, ok := exec.(runWatcherSetter); ok {
		setter.SetRunWatcher(m.watchRun)
	}
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
	for _, ts := range m.allTaskStates() {
		for _, ar := range ts.active {
			if ar.Run.ID == runID {
				ar.ForceKill = forceKill
				return
			}
		}
	}
}

// allTaskStates returns every taskState the manager currently tracks: live
// registry entries plus tasks RemoveTask evicted while a run was still
// draining. Bookkeeping that must reach every in-flight run regardless of
// removal (retirement lookup, force-kill, shutdown cancel/wait) uses this;
// name-based resolution must not — it reads m.tasks alone so a removed task
// is immediately unresolvable by name. Caller holds m.mu (either mode).
func (m *defaultTaskManager) allTaskStates() []*taskState {
	return slices.AppendSeq(slices.Collect(maps.Values(m.tasks)), maps.Values(m.removedTasks))
}

// taskStateFor looks up a taskState by name across both the live registry and
// tasks currently draining after removal. Caller holds m.mu (either mode).
func (m *defaultTaskManager) taskStateFor(name string) *taskState {
	if ts, ok := m.tasks[name]; ok {
		return ts
	}
	return m.removedTasks[name]
}

// UpsertTask adds a task if missing or replaces the existing definition.
func (m *defaultTaskManager) UpsertTask(task *model.Task) {
	// Deferred ahead of the lock so it runs after Unlock (see retireOrphaned).
	var orphanedRuns []*model.Run
	defer func() { m.retireOrphaned(orphanedRuns) }()
	m.mu.Lock()
	defer m.mu.Unlock()
	orphanedRuns = m.upsertTaskLocked(task)
}

// MutateTask atomically reads the named task's live definition, applies
// mutate to a copy, and re-installs it under one lock acquisition, so a
// concurrent reload or UpsertTask cannot land in between and be clobbered.
// found is false (mutate is never called) if the task is not currently
// registered by name; a task mid-removal-drain counts as not found, same as
// GetTask. A non-nil error from mutate aborts the write.
func (m *defaultTaskManager) MutateTask(taskName string, mutate func(*model.Task) error) (found bool, err error) {
	var orphanedRuns []*model.Run
	defer func() { m.retireOrphaned(orphanedRuns) }()
	m.mu.Lock()
	defer m.mu.Unlock()

	ts, exists := m.tasks[taskName]
	if !exists {
		return false, nil
	}
	taskCopy := *ts.task
	if mutateErr := mutate(&taskCopy); mutateErr != nil {
		return true, mutateErr
	}
	orphanedRuns = m.upsertTaskLocked(&taskCopy)
	return true, nil
}

// upsertTaskLocked installs or replaces task's definition and returns any
// queued runs orphaned by the change, for the caller to retire/publish once
// m.mu is released. Caller holds m.mu (write) and owns flushing the result.
func (m *defaultTaskManager) upsertTaskLocked(task *model.Task) []*model.Run {
	var orphanedRuns []*model.Run

	taskCopy := *task
	ts, exists := m.tasks[task.Name]
	if !exists {
		if draining, ok := m.removedTasks[task.Name]; ok {
			// Reviving a task a prior reload removed while a run was still
			// draining: reattach the same taskState (active list and all) so
			// the still-running instance keeps counting against concurrency and
			// instance limits until it retires.
			delete(m.removedTasks, task.Name)
			ts = draining
			ts.removed = false
		} else {
			ts = &taskState{}
		}
		m.tasks[task.Name] = ts
	}
	ts.task = &taskCopy

	if task.Kind.IsService() {
		m.upsertSupervisor(ts, task)
	} else if ts.supervisor != nil {
		// A service reloaded into a plain task: its supervisor stays behind
		// (late exits still report to it) but stopped as bookkeeping only, so
		// reviving the service later must restart it per Autostart, not leave
		// the stop in place.
		ts.supervisor.MarkStopped()
		ts.bookkeepingStop = true
	}

	if task.OnOverlap == model.PolicyQueue {
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
			go m.queueProcessLoop(ts)
		}
	} else if ts.cond != nil {
		// This task just flipped off queue policy. queueProcessLoop re-checks the
		// policy only around cond.Wait(), so without a signal it would park
		// forever and queued runs would stay 'pending' with nothing to start or
		// finalize them. Finalize the queue as RemoveTask does, then broadcast
		// unconditionally so a loop parked on an already-empty queue wakes too.
		orphanedRuns = m.finalizeOrphanedQueue(ts)
		ts.cond.Broadcast()
	}
	return orphanedRuns
}

// upsertSupervisor creates or updates ts's service supervisor for task.
// Caller holds m.mu.
func (m *defaultTaskManager) upsertSupervisor(ts *taskState, task *model.Task) {
	healthyAfter := config.OrDefault(task.HealthyAfter, config.DefaultHealthyAfter)
	if ts.supervisor == nil {
		ts.supervisor = services.NewSupervisor(task.Name, task.Instances, healthyAfter, !task.Autostart, m.clock)
		return
	}
	ts.supervisor.SetInstances(task.Instances)
	ts.supervisor.SetHealthyAfter(healthyAfter)
	// Reviving a service the daemon stopped only as bookkeeping (see
	// bookkeepingStop): resume it per the revived definition's Autostart, as
	// a brand-new supervisor would.
	if ts.bookkeepingStop {
		if task.Autostart {
			ts.supervisor.MarkRunning()
		}
		ts.bookkeepingStop = false
	}
}

// finalizeOrphanedQueue ends every run still sitting in ts.queue (a policy
// change or task removal left them with no drain loop to ever start them) and
// returns them for retireOrphaned, which must run once m.mu is released.
// Caller holds m.mu.
func (m *defaultTaskManager) finalizeOrphanedQueue(ts *taskState) []*model.Run {
	if len(ts.queue) == 0 {
		return nil
	}
	runs := make([]*model.Run, 0, len(ts.queue))
	for _, run := range ts.queue {
		m.endOrphanedPending(run)
		runs = append(runs, run)
	}
	ts.queue = nil
	return runs
}

// retireOrphaned retires queued runs finalizeOrphanedQueue ended from the
// jitter gate and publishes their terminal events. Callers defer it ahead of
// taking m.mu so it runs after Unlock: a queued run can be one the gate marked
// in-flight (gateMu -> mu lock order; see jitterGate's doc), and publishing
// while holding the write lock risks deadlocking a re-entrant subscriber (see
// TriggerRunWithOptions).
func (m *defaultTaskManager) retireOrphaned(runs []*model.Run) {
	for _, r := range runs {
		m.gate.onComplete(r.ID)
		m.publishTerminal(events.EventRunFailed, r, "")
	}
}

// RemoveTask drops a task from the manager when a reload removes it.
//
// The taskState is always evicted from the name-resolvable registry
// immediately — GetTask, TriggerRunWithOptions, and every service-control
// method stop resolving this name the instant this call returns, regardless
// of what is still in flight. This is what stops a station peer (or a delayed
// local restart/retry) from racing the drain window to act on a task that no
// longer exists in the operator's TOML.
//
// The queue-drain goroutine (if any) is woken and exits via the removed flag.
// For services, every live instance is cancelled and the supervisor is marked
// stopped so the exit handler does not refill the slots. Cron tasks keep their
// in-flight runs — those finish under the definition they captured, tracked in
// removedTasks until the last one retires (single-writer-per-task preserved).
func (m *defaultTaskManager) RemoveTask(taskName string) {
	// Deferred ahead of the lock so it runs after Unlock (see retireOrphaned).
	var orphanedRuns []*model.Run
	defer func() { m.retireOrphaned(orphanedRuns) }()
	m.mu.Lock()
	defer m.mu.Unlock()

	ts, exists := m.tasks[taskName]
	if !exists {
		return
	}
	ts.removed = true

	// Drop anything still queued and wake the drain goroutine so it returns.
	// Queued runs were already persisted as 'pending'; finalize them here so a
	// reload that removes the task never leaves a permanent non-terminal row.
	if ts.cond != nil {
		orphanedRuns = m.finalizeOrphanedQueue(ts)
		ts.cond.Broadcast()
	}

	// Services don't drain — cancel their instances and stop the supervisor so
	// the exit handler won't bring them back.
	if ts.task.Kind.IsService() && ts.supervisor != nil {
		ts.supervisor.MarkStopped()
		ts.bookkeepingStop = true
		for _, ar := range ts.active {
			ar.Cancel()
		}
	}

	delete(m.tasks, taskName)
	if len(ts.active) > 0 {
		m.removedTasks[taskName] = ts
	}
}

// ListServiceTasks returns copies of every registered service task. Used by the
// station integration to fold daemon-supervised services (notably station-declared
// ones, registered at runtime via service:apply and absent from the TOML
// snapshot) into the tasks.sync payload, so the station knows they are live here.
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

func (m *defaultTaskManager) TriggerRunWithOptions(taskName string, options TriggerRunOptions) (*model.Run, error) {
	m.mu.Lock()
	// A terminal event for a run that never executes is published after the
	// lock is released: EventRunFailed subscribers (the station bridge) re-enter
	// the manager via ServiceSnapshot → m.mu.RLock, which would deadlock if we
	// published while still holding the write lock. The deferred flush runs
	// after m.mu.Unlock (LIFO defer order). EventRunCreated stays inline — it
	// has no re-entrant subscriber and must precede run.started for the same run.
	var publishTerminal func()
	defer func() {
		if publishTerminal != nil {
			publishTerminal()
		}
	}()
	defer m.mu.Unlock()

	// Refuse new runs once shutdown has begun (see errShuttingDown). Shutdown
	// sets isShutdown before taking m.mu for its cancel pass, so checking it
	// here under the lock is race-free: a trigger either commits before
	// Shutdown's pass (and gets cancelled by it) or observes the flag and bails.
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

	var executionID *string
	if options.ExecutionID != "" {
		executionID = new(options.ExecutionID)
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

	isService := ts.task.Kind.IsService()
	var instanceIndex int
	if isService {
		if instanceIndex, err = ts.supervisor.Reserve(options.InstanceIndex); err != nil {
			return nil, err
		}
	}

	// A jittered cron run records CreatedAt as the tick it belongs to (set by
	// the scheduler), even though it starts later at tick + offset. Every other
	// path (services included) leaves ScheduledAt zero and stamps the current
	// clock.
	createdAt := m.clock()
	if !isService && !options.ScheduledAt.IsZero() {
		createdAt = options.ScheduledAt
	}

	run := &model.Run{
		ID:            ulid.Make().String(),
		ExecutionID:   executionID,
		TaskName:      taskName,
		Status:        model.PhasePending,
		TriggeredBy:   triggeredBy,
		CreatedAt:     createdAt,
		RetryAttempt:  options.RetryAttempt,
		RetryOfRunID:  options.RetryOfRunID,
		InstanceIndex: instanceIndex,
		Params:        resolvedParams,
	}

	m.persistence.PersistNew(run)
	m.publishRun(events.EventRunCreated, run, "")

	if !isService {
		action, actionErr := m.evaluateConcurrency(ts, run, ts.task.MaxConcurrentValue())
		switch action {
		case actionRejected:
			run.End(ts.task, model.ReasonSkipped, -1, m.clock())
			m.persistence.PersistExisting(run)
			publishTerminal = func() { m.publishTerminal(events.EventRunFailed, run, "") }
			return run.Copy(), actionErr
		case actionQueueFull:
			run.End(ts.task, model.ReasonQueueFull, -1, m.clock())
			m.persistence.PersistExisting(run)
			publishTerminal = func() { m.publishTerminal(events.EventRunFailed, run, "") }
			return run.Copy(), actionErr
		case actionQueued:
			// The run sits in the queue; queueProcessLoop will later hand it to a
			// goroutine. Return a snapshot so the caller never races that promotion.
			return run.Copy(), nil
		case actionStart:
			// PolicyKill: evaluateConcurrency already cancelled the oldest
			// run. Do NOT eagerly remove it from active here — let the goroutine
			// clean up after executor.Execute returns, so the concurrency count
			// stays accurate.
		}
	}

	// Snapshot before startRun hands the live run to its execution goroutine.
	// The caller must never read fields (Status, StartedAt, ...) that the run
	// goroutine concurrently writes, so it gets an independent copy.
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
//
// A fire can wait in the gate while the task becomes held, removed, or
// paused. RefreshCronHolds and reloads cannot reach into the gate to cancel it,
// so it is refused here; otherwise a held task would run twice (once here, once
// by the live system cron). Only this automatic path is guarded: manual/API
// triggers of a held task are still allowed.
func (m *defaultTaskManager) triggerJittered(taskName string, tick time.Time) (string, bool) {
	m.mu.RLock()
	ts, exists := m.tasks[taskName]
	// A removed task is never in m.tasks (RemoveTask evicts immediately), so
	// exists==true already implies not removed; only Held needs checking.
	stale := !exists || ts.task.Held()
	schedulePaused := m.schedulePaused
	m.mu.RUnlock()
	// A schedule paused while this fire waited in the gate refuses it the same
	// way. Asked outside m.mu: the scheduler's lock is a leaf (gate.mu →
	// scheduler.mutex), never taken with the manager lock held.
	if stale || (schedulePaused != nil && schedulePaused(taskName)) {
		return "", false
	}

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
// audit row, created and immediately ended "now" with the supplied reason.
func (m *defaultTaskManager) RecordSkippedFiring(taskName string, reason model.EndReason, triggeredBy model.TriggeredBy) error {
	return m.recordPhantomRun(taskName, m.clock(), reason, triggeredBy, "")
}

// RecordMissedRun persists a terminal end_reason = "missed" run that documents
// a cron downtime gap, then publishes a failure-level event whose RunEvent.Error
// carries the human sentence built by the catch-up detector. Unlike
// RecordSkippedFiring, the run's instant is the latest missed tick
// (scheduledAt), not now, so resolveCatchupAnchor reads it back as the
// last-alerted point and the next restart never re-alerts.
func (m *defaultTaskManager) RecordMissedRun(taskName string, scheduledAt time.Time, reason string) error {
	return m.recordPhantomRun(taskName, scheduledAt, model.ReasonMissed, model.TriggeredByCron, reason)
}

// recordPhantomRun builds a synthetic run that never executes — no process
// started, no log file, no streams open — persists it, and immediately ends
// it at the same instant it was created, for callers that need an audit row
// documenting a run that was suppressed or never fired. errText rides the
// terminal event as its RunEvent.Error (the notification body). The terminal
// event is published after m.mu is released (see TriggerRunWithOptions:
// publishing under the lock risks a deadlock if a subscriber re-enters).
func (m *defaultTaskManager) recordPhantomRun(taskName string, at time.Time, reason model.EndReason, triggeredBy model.TriggeredBy, errText string) error {
	m.mu.Lock()
	var run *model.Run
	defer func() {
		if run != nil {
			m.publishTerminal(events.EventRunFailed, run, errText)
		}
	}()
	defer m.mu.Unlock()

	ts, exists := m.tasks[taskName]
	if !exists {
		return fmt.Errorf(errTaskNotFoundFmt, taskName)
	}

	run = &model.Run{
		ID:          ulid.Make().String(),
		TaskName:    taskName,
		Status:      model.PhasePending,
		TriggeredBy: triggeredBy,
		CreatedAt:   at,
	}
	m.persistence.PersistNew(run)
	m.publishRun(events.EventRunCreated, run, "")
	run.End(ts.task, reason, -1, at)
	m.persistence.PersistExisting(run)
	return nil
}

// StartServiceInstances brings every instance of a service up to its desired
// count. Idempotent — already-running instances are left untouched. The
// triggeredBy argument labels the resulting runs: daemon boot passes
// TriggeredByService; an operator-initiated REST restart of a stopped service
// passes TriggeredByAPI.
func (m *defaultTaskManager) StartServiceInstances(taskName string, triggeredBy model.TriggeredBy) error {
	m.mu.RLock()
	ts, err := m.serviceLocked(taskName)
	if err != nil {
		m.mu.RUnlock()
		return err
	}
	if ts.supervisor.IsStopped() {
		m.mu.RUnlock()
		return nil
	}
	missing := ts.supervisor.MissingSlots()
	m.mu.RUnlock()

	for _, i := range missing {
		if _, err := m.TriggerRunWithOptions(taskName, TriggerRunOptions{
			TriggeredBy:   triggeredBy,
			InstanceIndex: &i,
		}); err != nil {
			slog.Error("Failed to start service instance", "task", taskName, "instance", i, "err", err)
		}
	}
	return nil
}

// serviceLocked looks up a service task, rejecting unknown names and
// non-services. Caller must hold m.mu (read or write).
func (m *defaultTaskManager) serviceLocked(taskName string) (*taskState, error) {
	ts, exists := m.tasks[taskName]
	if !exists {
		return nil, fmt.Errorf(errTaskNotFoundFmt, taskName)
	}
	if !ts.task.Kind.IsService() {
		return nil, fmt.Errorf(errTaskNotServiceFmt, taskName)
	}
	return ts, nil
}

// StartService clears a service's stop and FATAL flags, then brings it up to
// its desired instance count (StartServiceInstances alone no-ops on a stopped
// service). Already-live instances are left untouched; nothing is cancelled.
func (m *defaultTaskManager) StartService(taskName string) error {
	m.mu.Lock()
	ts, err := m.serviceLocked(taskName)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	wasStopped := ts.supervisor.IsStopped()
	ts.supervisor.MarkRunning()
	ts.bookkeepingStop = false
	m.mu.Unlock()

	if wasStopped {
		m.publishTasksChanged()
	}
	return m.StartServiceInstances(taskName, model.TriggeredByAPI)
}

// RestartServiceInstances brings a service back to its desired instance count.
// If the service was operator-stopped or had FATAL instances, the stop/FATAL
// flags are cleared and the empty slots are spawned with a fresh start-retry
// budget. If the service is already running, every active instance is cancelled
// and the exit handler refills the freed slots via the supervisor.
func (m *defaultTaskManager) RestartServiceInstances(taskName string) error {
	m.mu.Lock()
	ts, err := m.serviceLocked(taskName)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	// Capture FATAL state before MarkRunning clears it: a FATAL service has no
	// active runs in its dead slots, so they only come back if we spawn them.
	wasStopped := ts.supervisor.IsStopped()
	wasFatal := ts.supervisor.IsAnyFatal()
	ts.supervisor.MarkRunning()
	ts.bookkeepingStop = false
	for _, ar := range ts.active {
		ar.Cancel()
	}
	m.mu.Unlock()

	if wasStopped {
		m.publishTasksChanged()
	}
	if wasStopped || wasFatal {
		return m.StartServiceInstances(taskName, model.TriggeredByAPI)
	}
	return nil
}

// RecycleServiceInstances lets a reload-changed service definition (new
// command, env, or instance count) take effect without treating the reload
// as an operator restart: a stopped or never-autostarted service is left as
// it is, and a FATAL instance stays FATAL. When the service is running, every
// live instance is cancelled so the exit handler respawns it under the new
// definition, and any slots added by an instance-count increase are filled.
func (m *defaultTaskManager) RecycleServiceInstances(taskName string) error {
	m.mu.Lock()
	ts, err := m.serviceLocked(taskName)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if ts.supervisor.IsStopped() {
		m.mu.Unlock()
		return nil
	}
	for _, ar := range ts.active {
		ar.Cancel()
	}
	m.mu.Unlock()

	return m.StartServiceInstances(taskName, model.TriggeredByService)
}

// StopService marks the service as operator-stopped (in-memory only, cleared
// on daemon restart) and cancels every live instance. The exit handler honours
// the flag and stops refilling slots.
func (m *defaultTaskManager) StopService(taskName string) error {
	m.mu.Lock()
	ts, err := m.serviceLocked(taskName)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	wasStopped := ts.supervisor.IsStopped()
	ts.supervisor.MarkStopped()
	ts.bookkeepingStop = false
	for _, ar := range ts.active {
		ar.Cancel()
	}
	m.mu.Unlock()

	if !wasStopped {
		m.publishTasksChanged()
	}
	return nil
}

// publishTasksChanged tells dashboards to refetch /api/tasks after an operator
// start/stop flipped a service's stopped state (TaskResponse.ServiceStopped).
// Published here, not in the REST layer, so a station-driven stop shows up too.
// Callers must not hold m.mu: bus handlers run synchronously.
func (m *defaultTaskManager) publishTasksChanged() {
	m.eventBus.Publish(events.EventTasksChanged, events.TasksChangedEvent{})
}

// ServiceHealthy reports whether a service currently has at least one healthy
// instance (see services.Supervisor.IsHealthy). Non-services and unknown tasks
// report false. This is the live readiness signal consumed by depends_on boot
// gating.
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
// a condition variable: uptime-based readiness (healthy_after) crosses its
// threshold with no state change to signal on.
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
// reporting to station. ok is false when the task is unknown or not a service.
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

// startRun registers the run and spawns the execution goroutine. Assumes m.mu
// is held.
func (m *defaultTaskManager) startRun(task *model.Task, run *model.Run) {
	ctx, cancel := task.WithTimeout(context.Background())

	active := &ActiveRun{
		Run:       run,
		Cancel:    cancel,
		StartedAt: m.clock(),
	}

	m.tasks[task.Name].active = append(m.tasks[task.Name].active, active)

	m.wg.Add(1)
	go func() {
		defer crashguard.Guard()
		defer m.wg.Done()
		m.execute(ctx, task, run, active)
	}()
}

// execute drives one run through its lifecycle: mark running, hand off to the
// executor, record the outcome, then schedule any policy-driven follow-up.
func (m *defaultTaskManager) execute(ctx context.Context, task *model.Task, run *model.Run, active *ActiveRun) {
	// Under the manager lock: GetActiveRuns takes an RLock and copies these same
	// run fields, so writing them unlocked races with a concurrent snapshot.
	m.mu.Lock()
	run.Status = model.PhaseRunning
	run.StartedAt = &active.StartedAt
	if task.Kind.IsService() {
		// Stamp the live-readiness clock the moment the instance is running so
		// dependents gating on this service measure uptime from here. A run
		// started under a health_check is healthy only once watchRun says so.
		if ts := m.tasks[task.Name]; ts != nil && ts.supervisor != nil {
			ts.supervisor.MarkLive(run.InstanceIndex, task.HealthCheck != nil)
		}
	}
	m.mu.Unlock()
	m.persistence.PersistExisting(run)
	// Flush before the process spawns: LoadPendingRuns resumes any row still
	// 'pending' at boot, while MarkCrashedRuns fails any row 'running'. The row
	// must be durably 'running' before the OS process exists, or a crash could
	// leave it 'pending' and boot would spawn a second process for it.
	m.persistence.Flush()
	m.publishRun(events.EventRunStarted, run, "")

	result := m.executor.Execute(ctx, task, run)

	nextRestartAttempt, giveUp := m.recordRunOutcome(task, run, active, result)
	// Advance the jitter gate now the run is retired from active and the
	// manager lock is released — recordRunOutcome unlocks before returning, so
	// the gate may re-enter TriggerRunWithOptions without deadlocking. A no-op
	// for runs the gate never triggered.
	m.gate.onComplete(run.ID)
	if !giveUp {
		m.scheduleFollowup(task, run, nextRestartAttempt)
	}
}

// recordRunOutcome classifies the executor result, ends and persists the run,
// publishes the terminal event, and retires the run from the task's active set
// (refreshing supervisor bookkeeping for services). It returns the next
// restart-attempt counter for the run's instance — meaningful only for a
// restarting service, zero otherwise — and whether this run's restart chain
// has given up: a service instance marked FATAL. Either way the caller must
// not schedule another restart.
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
	run.OutputMatched = result.OutputMatched
	run.PeakMemoryBytes, run.CPUTimeMs = result.PeakMemoryBytes, result.CPUTimeMs
	run.End(task, outcome.endReason, result.ExitCode, endTime)

	m.persistence.PersistExisting(run)
	m.publishTerminal(outcome.eventType, run, "")

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
// counter (meaningful only for a restarting service, zero otherwise), whether
// a service instance is now FATAL, and the recorded start-fail count when
// FATAL (zero otherwise). Non-service tasks re-run via retry_* (see
// scheduleFollowup), which retireRun has no bookkeeping role in.
func (m *defaultTaskManager) retireRun(task *model.Task, run *model.Run, runDuration time.Duration, endReason model.EndReason) (nextRestartAttempt int, serviceFatal bool, fatalAttempts int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// This run is still in its taskState's active list, so the state has not
	// been reaped (reapRetiredTaskState only reaps at zero active) and is in
	// m.tasks or removedTasks.
	ts := m.taskStateFor(task.Name)
	for i, ar := range ts.active {
		if ar.Run.ID == run.ID {
			ts.active = append(ts.active[:i], ts.active[i+1:]...)
			break
		}
	}
	if task.Kind.IsService() {
		wasFailure := retry.IsFailedExecution(endReason)
		startRetries := config.OrDefault(task.RestartAttempts, config.DefaultStartRetries)
		nextRestartAttempt, serviceFatal = ts.supervisor.RecordExit(
			run.InstanceIndex, runDuration, startRetries, wasFailure)
		if serviceFatal {
			fatalAttempts = ts.supervisor.StartFails(run.InstanceIndex)
		}
	}
	if ts.cond != nil {
		ts.cond.Signal()
	}
	m.reapRetiredTaskState(task, ts)
	return nextRestartAttempt, serviceFatal, fatalAttempts
}

// reapRetiredTaskState drops a taskState once its last run has retired, for
// the two cases where nothing else will: a reload-removed task whose runs
// were still draining (tracked in removedTasks since RemoveTask), and an
// ephemeral station-inline task that never entered the TOML registry (tracked
// in tasks, since it was never removed). Caller must hold m.mu.
func (m *defaultTaskManager) reapRetiredTaskState(task *model.Task, ts *taskState) {
	if ts.removed && len(ts.active) == 0 {
		delete(m.removedTasks, task.Name)
	} else if ts.task != nil && ts.task.Ephemeral && len(ts.active) == 0 && len(ts.queue) == 0 {
		// Ephemeral station-inline tasks are one-shot and never enter the TOML
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
}

// scheduleFollowup spawns the retry or restart goroutine dictated by the task's
// policy after a run has ended. Station-triggered task runs never retry locally —
// the control plane owns their retry lifecycle. Services are the exception: the
// local supervisor keeps their instances alive no matter who started them.
// Service FATAL runs never reach this method — the caller guards on the FATAL
// flag returned by recordRunOutcome.
func (m *defaultTaskManager) scheduleFollowup(task *model.Task, run *model.Run, nextRestartAttempt int) {
	if run.TriggeredBy == model.TriggeredByStation && !task.Kind.IsService() {
		return
	}
	copiedRun := run.Copy()
	switch {
	case retry.ShouldRestart(task, run):
		m.wg.Add(1)
		go func() {
			defer crashguard.Guard()
			defer m.wg.Done()
			m.scheduleRestart(task, copiedRun, nextRestartAttempt)
		}()
	case retry.ShouldRetry(task, run):
		m.wg.Add(1)
		go func() {
			defer crashguard.Guard()
			defer m.wg.Done()
			m.scheduleRetry(task, copiedRun)
		}()
	}
}

type runOutcome struct {
	endReason model.EndReason
	eventType events.EventType
}

// publishRun publishes a run event. errMsg, when non-empty, rides the envelope
// as RunEvent.Error: a human-readable explanation for runs that never executed
// (e.g. a missed-run summary), surfaced as the notification body.
func (m *defaultTaskManager) publishRun(eventType events.EventType, run *model.Run, errMsg string) {
	// Publish guarantees event ordering: run.created always arrives before
	// run.started for the same run. The SSE handler's buffered channel
	// provides the async decoupling.
	m.eventBus.Publish(eventType, events.RunEvent{
		Run:   run.Copy(),
		Error: errMsg,
	})
}

// publishTerminal publishes a run's terminal event, but only once the terminal
// row enqueued just before it is readable from storage.
//
// Persistence is async (a buffered channel drained by one worker), so a bare
// publish outruns its own DB write: a subscriber that reads storage on hearing
// the event sees a stale non-terminal row. The SSE streamer sends `done` off
// this event and `runwisp run` then fetches the run, so a lagging row would
// report a failed run as "running" with exit code 0. Flush is the barrier; it
// costs one DB write per run, after the process has already exited.
func (m *defaultTaskManager) publishTerminal(eventType events.EventType, run *model.Run, errMsg string) {
	m.persistence.Flush()
	m.publishRun(eventType, run, errMsg)
}

// publishServiceFatal announces that a service instance exhausted its
// start-retry budget and the supervisor gave up restarting it. notify maps
// this to a SevError in-app bell + global notifiers so the give-up is loud,
// not silent.
func (m *defaultTaskManager) publishServiceFatal(taskName string, instanceIndex, attempts, lastExitCode int) {
	m.eventBus.Publish(events.EventServiceFatal, events.ServiceFatalEvent{
		TaskName:      taskName,
		InstanceIndex: instanceIndex,
		Attempts:      attempts,
		LastExitCode:  lastExitCode,
	})
}

// GetActiveRuns returns a snapshot of active runs for the given task. Each
// ActiveRun and its Run are copied so callers observe stable values — the live
// *ActiveRun.Run is concurrently mutated by the execute goroutine, so handing
// out the live pointer would race any caller reading Run's fields. The Cancel
// and ForceKill funcs are carried over unchanged, so a caller can still signal
// the underlying run.
func (m *defaultTaskManager) GetActiveRuns(taskName string) []*ActiveRun {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ts, exists := m.tasks[taskName]
	if !exists {
		return nil
	}
	runs := make([]*ActiveRun, len(ts.active))
	for i, ar := range ts.active {
		snapshot := *ar
		if ar.Run != nil {
			snapshot.Run = ar.Run.Copy()
		}
		runs[i] = &snapshot
	}
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

// WaitIdle blocks until taskName has no active runs, or returns ctx.Err()
// once ctx is done. Used to wait out a stop's graceful teardown before acting
// on the task again.
func WaitIdle(ctx context.Context, r TaskRunner, taskName string) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for r.GetActiveRunCount(taskName) > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	return nil
}

// StopTask cancels every active run of a non-service task and discards
// anything still queued, so nothing starts back up right behind the stop. The
// cron schedule is untouched. Returns an error for an unknown task or a service
// (use StopService for those). Idempotent, mirroring StopService.
// Cancelled runs end with ReasonStopped, which is outside retry eligibility
// (see runtime/retry.IsFailedExecution), so a stop never races its own
// automatic re-run.
func (m *defaultTaskManager) StopTask(taskName string) error {
	// Deferred ahead of the lock so it runs after Unlock (see retireOrphaned).
	var orphanedRuns []*model.Run
	defer func() { m.retireOrphaned(orphanedRuns) }()
	m.mu.Lock()
	defer m.mu.Unlock()

	ts, exists := m.tasks[taskName]
	if !exists {
		return fmt.Errorf(errTaskNotFoundFmt, taskName)
	}
	if ts.task.Kind.IsService() {
		return fmt.Errorf(errTaskIsServiceFmt, taskName)
	}
	for _, ar := range ts.active {
		ar.Cancel()
	}
	if ts.cond != nil {
		orphanedRuns = m.finalizeOrphanedQueue(ts)
		ts.cond.Broadcast()
	}
	return nil
}

// TerminateRun cancels a running task by ID.
func (m *defaultTaskManager) TerminateRun(runID string) error {
	return m.cancelActiveRun(func(ar *ActiveRun) bool {
		return ar.Run.ID == runID
	}, fmt.Sprintf("run not found: %s", runID))
}

// TerminateRunByExecutionID cancels a running run bound to an
// external execution ID.
func (m *defaultTaskManager) TerminateRunByExecutionID(executionID string) error {
	return m.cancelActiveRun(func(ar *ActiveRun) bool {
		return ar.Run.ExecutionID != nil && *ar.Run.ExecutionID == executionID
	}, fmt.Sprintf("run not found for external execution id: %s", executionID))
}

func (m *defaultTaskManager) cancelActiveRun(match func(*ActiveRun) bool, notFoundMsg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, ts := range m.allTaskStates() {
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

// BeginShutdown refuses every new run from here on (triggers, retries, queued
// runs, held jittered fires) and leaves active runs alone. The daemon calls it
// before stopping services; ShutdownWithDeadline calls it too. Idempotent.
func (m *defaultTaskManager) BeginShutdown() {
	m.isShutdown.Store(true)
	// Cancel before the wg drain so any goroutine parked in waitForDelay exits
	// now instead of holding the drain open.
	m.shutdownCancel()
	// Abandon pending jittered fires and stop their breach timers so no held
	// task starts a run after shutdown begins. Takes only the gate lock (no
	// manager lock held here), preserving the gateMu → mu order.
	m.gate.shutdown()
}

// ShutdownWithDeadline cancels every active run, waits up to deadline for
// goroutines to exit cleanly, and on timeout SIGKILLs survivors so the
// daemon can exit without leaving orphaned processes behind. Surviving runs
// are recorded with ReasonDaemonStopped via the deadlineExceeded flag.
// deadline <= 0 means "wait indefinitely".
func (m *defaultTaskManager) ShutdownWithDeadline(deadline time.Duration) {
	m.BeginShutdown()
	m.mu.Lock()
	for _, ts := range m.allTaskStates() {
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
	for _, ts := range m.allTaskStates() {
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
