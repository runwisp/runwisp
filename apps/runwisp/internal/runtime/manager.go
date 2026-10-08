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
	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/executor"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/runtime/services"
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
	// schedule; set by SetSchedulePaused (nil until then). Consulted only for a
	// jittered fire already waiting in the gate (see triggerJittered).
	schedulePaused func(string) bool
	// daemonLocation is the [daemon] timezone a health check cron with no
	// timezone of its own runs in; nil means time.Local. Set at boot in every
	// mode and on a reload that changes it (SetDaemonLocation).
	daemonLocation *time.Location
}

// SetSchedulePaused wires the scheduler's pause check (Scheduler.IsPaused).
func (m *defaultTaskManager) SetSchedulePaused(fn func(string) bool) {
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

	exec.SetRunWatcher(m.watchRun)
	return m
}

// BindPersistenceHook wires persistence to both the manager and executor.
// The executor's process-started callback lets the manager reach each active
// run's ForceKill closure during shutdown.
func (m *defaultTaskManager) BindPersistenceHook(hook RunPersistenceHook) {
	m.persistence.BindHook(hook)
	m.executor.SetRunUpdateCallback(m.persistence.PersistExisting)
	m.executor.SetOnProcessStarted(m.registerForceKill)
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
	healthyAfter := model.OrDefault(task.HealthyAfter, config.DefaultHealthyAfter)
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
