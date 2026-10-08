// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/crashguard"
	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/executor"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/runtime/retry"
)

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
		startRetries := model.OrDefault(task.RestartAttempts, config.DefaultStartRetries)
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
func WaitIdle(ctx context.Context, r interface{ GetActiveRunCount(string) int }, taskName string) error {
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
