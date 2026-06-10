// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func testTask(name string, policy model.ConcurrencyPolicy, limit int) *model.Task {
	return &model.Task{
		Name:          name,
		MaxConcurrent: limit,
		OnOverlap:     policy,
	}
}

func TestUpsertTask(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)

	task := testTask("task1", model.PolicyQueue, DefaultConcurrencyLimit)
	jm.UpsertTask(task)

	djm := jm.(*defaultTaskManager)
	assert.Contains(t, djm.tasks, "task1")
	assert.NotNil(t, djm.tasks["task1"].cond)
}

func TestTriggerRunBasic(t *testing.T) {
	jm, exec, eb := newTestManager(t)

	task := testTask("task1", model.PolicySkip, 1)
	jm.UpsertTask(task)

	exec.On("Execute", mock.Anything, task, mock.Anything).Return(&executor.ExecuteResult{ExitCode: 0})

	done := watchCompletions(eb)
	run, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	assert.NoError(t, err)
	assert.NotNil(t, run)

	done.waitFor(t, 1)
	exec.AssertExpectations(t)
}

func TestPolicySkip(t *testing.T) {
	jm, exec, _ := newGatedManager(t)

	task := testTask("task1", model.PolicySkip, 1)
	jm.UpsertTask(task)

	// First run holds the only slot until the test releases it.
	_, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	assert.NoError(t, err)
	exec.WaitStarted(t)

	// Second run should skip and persist with end_reason="skipped" — the skip
	// policy is working as intended, so it must not pose as a failure to
	// retries, notifications, or stats.
	run2, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	assert.Error(t, err)
	assert.Equal(t, "task already running, skipping (policy: skip)", err.Error())
	assert.Equal(t, model.PhaseEnded, run2.Status)
	assert.Equal(t, model.EndReasonPtr(model.ReasonSkipped), run2.EndReason)
	assert.Equal(t, -1, run2.ExitCode)
	assert.False(t, run2.IsRetryable(), "skipped runs must not be flagged as retryable")
}

func TestPolicyQueue(t *testing.T) {
	jm, exec, eb := newGatedManager(t)

	task := testTask("task1", model.PolicyQueue, 1)
	jm.UpsertTask(task)

	done := watchCompletions(eb)

	// First run holds the only slot.
	_, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	assert.NoError(t, err)
	exec.WaitStarted(t)

	// Second run should queue behind it.
	run2, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	assert.NoError(t, err)
	assert.Equal(t, model.PhasePending, run2.Status)

	// Release: first finishes, second dequeues and runs. Both must execute.
	exec.ReleaseAll()
	done.waitFor(t, 2)
	assert.Equal(t, 2, exec.Calls())
}

// TestPolicyQueueDropsAtCap exercises the new queue_max bound: once the
// pending queue holds queue_max runs, the next firing is recorded with
// end_reason = "queue_full" rather than growing the queue without bound.
func TestPolicyQueueDropsAtCap(t *testing.T) {
	jm, exec, _ := newGatedManager(t)

	task := testTask("task1", model.PolicyQueue, 1)
	task.QueueMax = 1
	jm.UpsertTask(task)

	first, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	require.NoError(t, err)
	require.Equal(t, model.PhasePending, first.Status)
	// Once first is executing it holds the slot and the queue is empty.
	exec.WaitStarted(t)

	// Second firing occupies the queue (fills queue_max = 1).
	queued, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	require.NoError(t, err)
	require.Equal(t, model.PhasePending, queued.Status)

	// Third firing trips queue_max and is dropped immediately.
	dropped, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	require.Error(t, err, "third firing should be rejected")
	assert.Contains(t, err.Error(), "queue full")
	assert.Equal(t, model.PhaseEnded, dropped.Status)
	require.NotNil(t, dropped.EndReason)
	assert.Equal(t, model.ReasonQueueFull, *dropped.EndReason)
}

func TestPolicyTerminate(t *testing.T) {
	jm, exec, eb := newGatedManager(t)

	task := testTask("task1", model.PolicyTerminate, 1)
	jm.UpsertTask(task)

	done := watchCompletions(eb)

	_, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	assert.NoError(t, err)
	exec.WaitStarted(t) // run1 holds the slot

	// Second run should terminate the first, then take the slot.
	_, err = jm.TriggerRun("task1", model.TriggeredByAPI)
	assert.NoError(t, err)

	// run1 is cancelled by the terminate; release so run2 finishes too.
	exec.ReleaseAll()
	done.waitFor(t, 2)
	assert.Equal(t, 2, exec.Calls())
}

func TestTerminateRun(t *testing.T) {
	jm, exec, eb := newGatedManager(t)

	task := testTask("task1", model.PolicySkip, 1)
	jm.UpsertTask(task)

	done := watchCompletions(eb)
	run, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	assert.NoError(t, err)
	exec.WaitStarted(t)

	err = jm.TerminateRun(run.ID)
	assert.NoError(t, err)

	// Cancelling the run's context unblocks the gated executor; the run must
	// reach a terminal state.
	done.waitFor(t, 1)
}

func TestShutdown(t *testing.T) {
	exec := testutil.NewGateExecutor()
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)

	task := testTask("task1", model.PolicyQueue, 1)
	jm.UpsertTask(task)

	_, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	require.NoError(t, err)
	exec.WaitStarted(t)

	// Shutdown must cancel the in-flight run and return without panicking.
	jm.Shutdown()
}

// TestTriggerRefusedAfterShutdown pins the invariant that no run may start once
// shutdown has begun. Without it a restart/retry that races Shutdown could
// append a run after Shutdown's single cancel pass, leaving its context live
// forever — an orphaned process and a drain that never completes. The gated
// service tests exercise the racing path; this one nails the contract directly.
func TestTriggerRefusedAfterShutdown(t *testing.T) {
	exec := testutil.NewGateExecutor()
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)
	jm.UpsertTask(testTask("task1", model.PolicySkip, 1))

	jm.Shutdown()

	_, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	require.ErrorIs(t, err, errShuttingDown, "a trigger after shutdown must be refused, not started")
}

// TestShutdownDoesNotPromoteQueuedRun is the regression test for a graceful-
// shutdown bug: when Shutdown cancels the active run, the freed slot must not
// cause queueProcessLoop to promote a queued run. A run started after the
// single cancel pass would never have its context cancelled — an orphaned
// process and a drain that never completes. Shutdown is run with a watchdog so
// a regression fails fast with a clear message instead of hanging the suite.
func TestShutdownDoesNotPromoteQueuedRun(t *testing.T) {
	jm, exec, _ := newGatedManager(t)

	task := testTask("task1", model.PolicyQueue, 1)
	jm.UpsertTask(task)

	_, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	require.NoError(t, err)
	exec.WaitStarted(t) // run holds the only slot; the gate keeps it in flight

	_, err = jm.TriggerRun("task1", model.TriggeredByAPI)
	require.NoError(t, err) // this run sits in the queue

	done := make(chan struct{})
	go func() {
		jm.Shutdown()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown hung: a queued run was promoted after the cancel pass and orphaned")
	}

	assert.Equal(t, 1, exec.Calls(), "the queued run must not be promoted during shutdown")
}

// TestTriggerRunReturnsIndependentSnapshot guards the contract that the run
// returned by TriggerRun is a snapshot, not the live pointer the execution
// goroutine mutates. A caller (e.g. the REST trigger handler) reads the
// returned run to build its response; sharing the live pointer would race the
// goroutine's Status write. After WaitStarted the live run is PhaseRunning, so
// a snapshot that still reads PhasePending proves the copy is independent — and
// this holds without -race, so plain CI catches a regression.
func TestTriggerRunReturnsIndependentSnapshot(t *testing.T) {
	jm, exec, _ := newGatedManager(t)
	jm.UpsertTask(testTask("task1", model.PolicySkip, 1))

	r, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	require.NoError(t, err)
	exec.WaitStarted(t) // the execution goroutine has now set the live run to PhaseRunning

	assert.Equal(t, model.PhasePending, r.Status,
		"returned run must be an independent snapshot, unaffected by the run goroutine")
}

// stuckExecutor blocks Execute until its ForceKill closure runs, ignoring
// context cancellation. Used to drive the daemon-shutdown deadline path:
// the manager's per-task Cancel has no effect, so the only way out is the
// deadline-triggered ForceKill.
type stuckExecutor struct {
	onStarted     func(runID string, forceKill func())
	startedCh     chan struct{}
	forceKilledCh chan struct{}
	startOnce     sync.Once
	killOnce      sync.Once
}

func (s *stuckExecutor) SetOnProcessStarted(cb func(runID string, forceKill func())) {
	s.onStarted = cb
}

func (s *stuckExecutor) Availability() executor.Availability { return executor.Availability{} }

func (s *stuckExecutor) Execute(_ context.Context, _ *model.Task, run *model.Run) *executor.ExecuteResult {
	released := make(chan struct{})
	if s.onStarted != nil {
		s.onStarted(run.ID, func() {
			s.killOnce.Do(func() {
				close(s.forceKilledCh)
				close(released)
			})
		})
	}
	s.startOnce.Do(func() { close(s.startedCh) })
	select {
	case <-released:
	case <-time.After(2 * time.Second):
	}
	return &executor.ExecuteResult{ExitCode: -1, Stopped: true}
}

// TestShutdownWithDeadlineMarksSurvivorsDaemonStopped verifies the daemon
// shutdown coordinator: when a run ignores context cancellation, the
// shutdown deadline must fire ForceKill and the surviving run must be
// recorded with end_reason = "daemon_stopped".
func TestShutdownWithDeadlineMarksSurvivorsDaemonStopped(t *testing.T) {
	exec := &stuckExecutor{
		startedCh:     make(chan struct{}),
		forceKilledCh: make(chan struct{}),
	}
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)

	var mu sync.Mutex
	var persisted []*model.Run
	jm.BindPersistenceHook(func(_ context.Context, run *model.Run, _ bool) {
		mu.Lock()
		defer mu.Unlock()
		persisted = append(persisted, run)
	})

	task := testTask("stuck", model.PolicySkip, 1)
	jm.UpsertTask(task)

	_, err := jm.TriggerRun("stuck", model.TriggeredByAPI)
	require.NoError(t, err)

	select {
	case <-exec.startedCh:
	case <-time.After(time.Second):
		t.Fatal("executor never reached the running state")
	}

	jm.ShutdownWithDeadline(50 * time.Millisecond)

	select {
	case <-exec.forceKilledCh:
	case <-time.After(time.Second):
		t.Fatal("ForceKill must have been invoked by the shutdown deadline")
	}

	// ShutdownWithDeadline drains the persistence worker before returning (it
	// calls persistence.Shutdown after the execute goroutine's wg.Done), so the
	// daemon_stopped write has already been applied — no sleep required.

	mu.Lock()
	defer mu.Unlock()
	var sawDaemonStopped bool
	for _, r := range persisted {
		if r.EndReason != nil && *r.EndReason == model.ReasonDaemonStopped {
			sawDaemonStopped = true
			break
		}
	}
	assert.True(t, sawDaemonStopped, "shutdown survivor must be recorded with ReasonDaemonStopped")
}

func TestRecordMissedRun(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)

	var mu sync.Mutex
	persisted := make([]*model.Run, 0, 2)
	jm.BindPersistenceHook(func(_ context.Context, run *model.Run, _ bool) {
		mu.Lock()
		defer mu.Unlock()
		persisted = append(persisted, run.Copy())
	})

	var sawCreated bool
	var failedErr string
	var failedReason *model.EndReason
	eb.Subscribe(events.EventRunCreated, func(e events.Event) {
		mu.Lock()
		defer mu.Unlock()
		sawCreated = true
		_ = e
	})
	eb.Subscribe(events.EventRunFailed, func(e events.Event) {
		mu.Lock()
		defer mu.Unlock()
		re, ok := e.Data.(events.RunEvent)
		if !ok {
			return
		}
		failedErr = re.Error
		if re.Run != nil {
			failedReason = re.Run.EndReason
		}
	})

	task := testTask("nightly", model.PolicyQueue, 1)
	jm.UpsertTask(task)

	scheduledAt := time.Date(2026, 6, 9, 3, 0, 0, 0, time.UTC)
	const reason = "3 scheduled runs missed since 2026-06-09 03:00 (daemon was down)"
	require.NoError(t, jm.RecordMissedRun("nightly", scheduledAt, reason))

	// Persistence is drained by an async worker; the event publishes are inline.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(persisted) == 2
	}, time.Second, 5*time.Millisecond, "missed run is persisted as create + terminal update")

	mu.Lock()
	defer mu.Unlock()

	final := persisted[len(persisted)-1]
	assert.Equal(t, model.PhaseEnded, final.Status)
	require.NotNil(t, final.EndReason)
	assert.Equal(t, model.ReasonMissed, *final.EndReason)
	assert.Equal(t, -1, final.ExitCode)
	assert.Equal(t, model.TriggeredByCron, final.TriggeredBy)
	assert.True(t, scheduledAt.Equal(final.CreatedAt),
		"CreatedAt must be the scheduled tick (the next-restart anchor): want %s, got %s", scheduledAt, final.CreatedAt)

	assert.True(t, sawCreated, "RecordMissedRun must publish EventRunCreated")
	require.NotNil(t, failedReason, "RecordMissedRun must publish EventRunFailed carrying the run")
	assert.Equal(t, model.ReasonMissed, *failedReason)
	assert.Equal(t, reason, failedErr,
		"the human sentence must ride the event as RunEvent.Error so it renders as the notification body")
}

func TestRecordMissedRunUnknownTask(t *testing.T) {
	jm := NewTaskManager(new(testutil.MockExecutor), events.NewEventBus(), time.Now)
	err := jm.RecordMissedRun("ghost", time.Date(2026, 6, 9, 3, 0, 0, 0, time.UTC), "irrelevant")
	require.Error(t, err, "recording a miss for an unknown task is an error, not a silent no-op")
}

func TestPersistenceHook(t *testing.T) {
	jm, exec, eb := newTestManager(t)

	var created, updated atomic.Bool
	jm.BindPersistenceHook(func(_ context.Context, run *model.Run, isNew bool) {
		if isNew {
			created.Store(true)
		} else {
			updated.Store(true)
		}
	})

	task := testTask("task1", model.PolicySkip, 1)
	jm.UpsertTask(task)

	exec.On("Execute", mock.Anything, task, mock.Anything).Return(&executor.ExecuteResult{ExitCode: 0})

	done := watchCompletions(eb)
	jm.TriggerRun("task1", model.TriggeredByAPI)
	done.waitFor(t, 1)
	// The final (update) persist is enqueued just before the completion event;
	// Flush guarantees the worker has applied every queued write.
	jm.persistence.Flush()

	assert.True(t, created.Load())
	assert.True(t, updated.Load())
}

// TestRetryFiresOnFailure exercises the retry path end-to-end: a failed
// run must trigger a follow-up run with RetryAttempt incremented and
// RetryOfRunID pointing back at the original.
func TestRetryFiresOnFailure(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)
	defer jm.Shutdown()

	task := &model.Task{
		Name:          "task1",
		Run:           "echo hi",
		MaxConcurrent: 1,
		OnOverlap:     model.PolicySkip,
		RetryAttempts: 2,
		RetryDelay:    5 * time.Millisecond,
	}
	jm.UpsertTask(task)

	var calls atomic.Int32
	exec.On("Execute", mock.Anything, task, mock.Anything).Run(func(args mock.Arguments) {
		calls.Add(1)
	}).Return(&executor.ExecuteResult{ExitCode: 1})

	var (
		mu   sync.Mutex
		runs []*model.Run
	)
	jm.BindPersistenceHook(func(_ context.Context, r *model.Run, isNew bool) {
		if !isNew {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		runs = append(runs, r)
	})

	_, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	require.NoError(t, err)

	// Initial + 2 retries = 3 calls.
	require.Eventually(t, func() bool {
		return calls.Load() >= 3
	}, time.Second, 10*time.Millisecond, "expected 3 executions")

	// Allow any spurious retry to surface, then cap.
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(3), calls.Load(), "retry must stop at attempt budget")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, runs, 3, "should have created original + 2 retry runs")
	assert.Equal(t, 0, runs[0].RetryAttempt)
	assert.Nil(t, runs[0].RetryOfRunID)

	assert.Equal(t, 1, runs[1].RetryAttempt)
	require.NotNil(t, runs[1].RetryOfRunID)
	assert.Equal(t, runs[0].ID, *runs[1].RetryOfRunID)

	assert.Equal(t, 2, runs[2].RetryAttempt)
	require.NotNil(t, runs[2].RetryOfRunID)
	assert.Equal(t, runs[1].ID, *runs[2].RetryOfRunID)
}

// TestRetrySkippedForCloudRun pins the rule that cloud-triggered runs are
// retried by the control plane, never by the local daemon.
func TestRetrySkippedForCloudRun(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)
	defer jm.Shutdown()

	task := &model.Task{
		Name:          "task1",
		Run:           "echo hi",
		MaxConcurrent: 1,
		OnOverlap:     model.PolicySkip,
		RetryAttempts: 3,
		RetryDelay:    5 * time.Millisecond,
	}
	jm.UpsertTask(task)

	var calls atomic.Int32
	exec.On("Execute", mock.Anything, task, mock.Anything).Run(func(args mock.Arguments) {
		calls.Add(1)
	}).Return(&executor.ExecuteResult{ExitCode: 1})

	_, err := jm.TriggerRun("task1", model.TriggeredByCloud)
	require.NoError(t, err)

	// One initial run, then long enough for a retry to fire if it were going
	// to.
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, int32(1), calls.Load(), "cloud-triggered runs must not retry locally")
}

// TestRetryNotFiredWhenRestartPolicySet pins the precedence: when a task has
// both retry and restart configured, restart wins and retry is suppressed.
func TestRetryNotFiredWhenRestartPolicySet(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)
	defer jm.Shutdown()

	task := &model.Task{
		Name:          "task1",
		Run:           "echo hi",
		MaxConcurrent: 1,
		OnOverlap:     model.PolicySkip,
		Restart:       model.RestartOnFailure,
		RestartDelay:  5 * time.Millisecond,
		RetryAttempts: 3,
		RetryDelay:    5 * time.Millisecond,
	}
	jm.UpsertTask(task)

	var calls atomic.Int32
	exec.On("Execute", mock.Anything, task, mock.Anything).Run(func(args mock.Arguments) {
		calls.Add(1)
	}).Return(&executor.ExecuteResult{ExitCode: 1})

	_, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	require.NoError(t, err)

	// Wait for at least 4 calls — restart loops indefinitely, retry would cap
	// at 4 (initial + 3 retries) and then stop. Watching for >=5 confirms the
	// restart path took over.
	require.Eventually(t, func() bool {
		return calls.Load() >= 5
	}, time.Second, 10*time.Millisecond, "restart should keep firing past retry budget")
}

// TestLoadPendingRunsResumed: a pending cron task with a free slot is
// resumed and starts execution.
func TestLoadPendingRunsResumed(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)
	defer jm.Shutdown()

	task := testTask("task1", model.PolicySkip, 1)
	jm.UpsertTask(task)

	var calls atomic.Int32
	exec.On("Execute", mock.Anything, task, mock.Anything).Run(func(args mock.Arguments) {
		calls.Add(1)
	}).Return(&executor.ExecuteResult{ExitCode: 0}, 50*time.Millisecond)

	pending := []model.Run{
		{ID: "01", TaskName: "task1", Status: model.PhasePending},
	}
	result := jm.LoadPendingRuns(pending)

	assert.Equal(t, 1, result.Resumed)
	assert.Equal(t, 0, result.Queued)
	assert.Equal(t, 0, result.Failed)
	assert.Equal(t, 0, result.Skipped)

	require.Eventually(t, func() bool {
		return calls.Load() >= 1
	}, time.Second, 10*time.Millisecond)
}

// TestLoadPendingRunsQueued: every pending run on a queue-policy task is
// pushed onto the per-task queue. The queue processor drains them as slots
// open, so they all count as Queued (not Resumed).
func TestLoadPendingRunsQueued(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)
	defer jm.Shutdown()

	task := testTask("task1", model.PolicyQueue, 1)
	jm.UpsertTask(task)

	exec.On("Execute", mock.Anything, task, mock.Anything).Return(
		&executor.ExecuteResult{ExitCode: 0}, 10*time.Millisecond,
	)

	pending := []model.Run{
		{ID: "01", TaskName: "task1", Status: model.PhasePending},
		{ID: "02", TaskName: "task1", Status: model.PhasePending},
		{ID: "03", TaskName: "task1", Status: model.PhasePending},
	}
	result := jm.LoadPendingRuns(pending)

	assert.Equal(t, 0, result.Resumed)
	assert.Equal(t, 3, result.Queued)
	assert.Equal(t, 0, result.Failed)
}

// TestLoadPendingRunsFailedWhenSlotFull: a non-queue policy with no free
// slot marks the pending run as failed.
func TestLoadPendingRunsFailedWhenSlotFull(t *testing.T) {
	jm, exec, _ := newGatedManager(t)

	task := testTask("task1", model.PolicySkip, 1)
	jm.UpsertTask(task)

	// Trigger one run that holds the only slot.
	_, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	require.NoError(t, err)
	exec.WaitStarted(t)

	pending := []model.Run{
		{ID: "01", TaskName: "task1", Status: model.PhasePending},
		{ID: "02", TaskName: "task1", Status: model.PhasePending},
	}
	result := jm.LoadPendingRuns(pending)

	assert.Equal(t, 0, result.Resumed)
	assert.Equal(t, 0, result.Queued)
	assert.Equal(t, 2, result.Failed, "skip-policy + full slots → mark pending as failed")
}

// TestLoadPendingRunsSkippedTaskNotFound: pending runs whose task is no
// longer in the config are dropped as skipped.
func TestLoadPendingRunsSkippedTaskNotFound(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)
	defer jm.Shutdown()

	pending := []model.Run{
		{ID: "01", TaskName: "ghost", Status: model.PhasePending},
		{ID: "02", TaskName: "ghost", Status: model.PhasePending},
	}
	result := jm.LoadPendingRuns(pending)

	assert.Equal(t, 2, result.Skipped)
	assert.Equal(t, 0, result.Resumed)
	assert.Equal(t, 0, result.Queued)
	assert.Equal(t, 0, result.Failed)
}

func TestResolveRunOutcomeKilledByPolicy(t *testing.T) {
	cases := []struct {
		name       string
		result     executor.ExecuteResult
		wantReason model.EndReason
	}{
		{"policy kill records as log_overflow", executor.ExecuteResult{ExitCode: -1, Stopped: true, KilledByPolicy: true}, model.ReasonLogOverflow},
		{"clean stop stays stopped", executor.ExecuteResult{ExitCode: -1, Stopped: true}, model.ReasonStopped},
		{"timeout still wins over policy", executor.ExecuteResult{ExitCode: -1, TimedOut: true, KilledByPolicy: true}, model.ReasonTimeout},
		{"success unaffected", executor.ExecuteResult{ExitCode: 0}, model.ReasonSuccess},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.result.EndReason()
			assert.Equal(t, tc.wantReason, got)
		})
	}
}

func TestPersistAfterShutdownDoesNotPanic(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)

	jm.BindPersistenceHook(func(_ context.Context, run *model.Run, isNew bool) {})
	jm.Shutdown()

	djm := jm.(*defaultTaskManager)
	assert.NotPanics(t, func() {
		djm.persistence.PersistExisting(&model.Run{})
	})
}

func TestGetTask_NotFound(t *testing.T) {
	jm := NewTaskManager(new(testutil.MockExecutor), events.NewEventBus(), time.Now)
	_, found := jm.GetTask("nonexistent")
	assert.False(t, found)
}

func TestGetTask_Found(t *testing.T) {
	jm := NewTaskManager(new(testutil.MockExecutor), events.NewEventBus(), time.Now)
	task := testTask("task1", model.PolicySkip, 1)
	jm.UpsertTask(task)
	got, found := jm.GetTask("task1")
	assert.True(t, found)
	assert.Equal(t, "task1", got.Name)
}

func TestGetActiveRuns_NotFound(t *testing.T) {
	jm := NewTaskManager(new(testutil.MockExecutor), events.NewEventBus(), time.Now)
	runs := jm.GetActiveRuns("unknown")
	assert.Nil(t, runs)
}

// TestGetActiveRuns_KnownTaskReturnsCopy verifies the positive path: a known
// task with one active run returns a non-empty slice and the slice is a copy
// (mutating it does not affect the manager's internal state).
func TestGetActiveRuns_KnownTaskReturnsCopy(t *testing.T) {
	jm, exec, _ := newGatedManager(t)

	task := testTask("task1", model.PolicySkip, 1)
	jm.UpsertTask(task)
	_, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	require.NoError(t, err)
	exec.WaitStarted(t)

	runs := jm.GetActiveRuns("task1")
	require.Len(t, runs, 1)
	// Mutating the returned slice does not affect manager state.
	runs[0] = nil
	assert.Len(t, jm.GetActiveRuns("task1"), 1)
}

// TestNewTaskManager_NilClockFallsBackToTimeNow guards the nil-clock branch,
// which production code must never rely on but exists as a defensive default.
func TestNewTaskManager_NilClockFallsBackToTimeNow(t *testing.T) {
	jm := NewTaskManager(new(testutil.MockExecutor), events.NewEventBus(), nil)
	defer jm.Shutdown()

	dm, ok := jm.(*defaultTaskManager)
	require.True(t, ok)
	require.NotNil(t, dm.clock)
	assert.WithinDuration(t, time.Now(), dm.clock(), time.Second)
}

func TestGetActiveRunCount_UnknownTaskIsZero(t *testing.T) {
	jm := NewTaskManager(new(testutil.MockExecutor), events.NewEventBus(), time.Now)
	assert.Equal(t, 0, jm.GetActiveRunCount("unknown"))
}

func TestGetActiveRunCount_KnownTaskWithNoActiveIsZero(t *testing.T) {
	jm := NewTaskManager(new(testutil.MockExecutor), events.NewEventBus(), time.Now)
	defer jm.Shutdown()
	jm.UpsertTask(testTask("idle", model.PolicySkip, 1))
	assert.Equal(t, 0, jm.GetActiveRunCount("idle"))
}

func TestTerminateRunByExternalExecutionID_NotFound(t *testing.T) {
	jm := NewTaskManager(new(testutil.MockExecutor), events.NewEventBus(), time.Now)
	err := jm.TerminateRunByExternalExecutionID("unknown-ext-id")
	assert.Error(t, err)
}

// TestTerminateRunByExternalExecutionID_MatchesActiveRun exercises the positive
// path: a run triggered with an ExternalExecutionID is cancelled by passing the
// same external ID into TerminateRunByExternalExecutionID.
func TestTerminateRunByExternalExecutionID_MatchesActiveRun(t *testing.T) {
	jm, exec, _ := newGatedManager(t)

	task := testTask("task1", model.PolicySkip, 1)
	jm.UpsertTask(task)

	_, err := jm.TriggerRunWithOptions("task1", TriggerRunOptions{
		TriggeredBy:         model.TriggeredByCloud,
		ExternalExecutionID: "ext-123",
	})
	require.NoError(t, err)

	exec.WaitStarted(t) // the run is active and holds its external ID
	require.NoError(t, jm.TerminateRunByExternalExecutionID("ext-123"))
}

func TestStopService_TaskNotFound(t *testing.T) {
	jm := NewTaskManager(new(testutil.MockExecutor), events.NewEventBus(), time.Now)
	defer jm.Shutdown()
	err := jm.StopService("missing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing")
}

func TestStopService_NotAService(t *testing.T) {
	jm := NewTaskManager(new(testutil.MockExecutor), events.NewEventBus(), time.Now)
	defer jm.Shutdown()
	jm.UpsertTask(testTask("cron", model.PolicySkip, 1))
	err := jm.StopService("cron")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a service")
}

func TestRestartServiceInstances_TaskNotFound(t *testing.T) {
	jm := NewTaskManager(new(testutil.MockExecutor), events.NewEventBus(), time.Now)
	defer jm.Shutdown()
	err := jm.RestartServiceInstances("missing")
	require.Error(t, err)
}

func TestStartServiceInstances_TaskNotFound(t *testing.T) {
	jm := NewTaskManager(new(testutil.MockExecutor), events.NewEventBus(), time.Now)
	defer jm.Shutdown()
	err := jm.StartServiceInstances("missing", model.TriggeredByService)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing")
}

func TestStartServiceInstances_NotAServiceTask(t *testing.T) {
	jm := NewTaskManager(new(testutil.MockExecutor), events.NewEventBus(), time.Now)
	defer jm.Shutdown()
	jm.UpsertTask(testTask("cron", model.PolicySkip, 1))
	err := jm.StartServiceInstances("cron", model.TriggeredByService)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a service")
}

// TestStartServiceInstances_StoppedServiceIsNoop verifies the early-return
// branch when the supervisor's stop flag is set: no instances start and no
// error is returned.
func TestStartServiceInstances_StoppedServiceIsNoop(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)
	defer jm.Shutdown()

	task := &model.Task{
		Name:           "svc",
		Kind:           model.KindService,
		Run:            "echo hi",
		Restart:        model.RestartAlways,
		MaxConcurrent:  1,
		OnOverlap:      model.PolicySkip,
		Instances:      2,
		RestartDelay:   time.Millisecond,
		RestartBackoff: model.BackoffConstant,
	}
	jm.UpsertTask(task)
	require.NoError(t, jm.StopService("svc"))

	require.NoError(t, jm.StartServiceInstances("svc", model.TriggeredByService))
	exec.AssertNotCalled(t, "Execute", mock.Anything, mock.Anything, mock.Anything)
}

// TestInjectedClockStampsCreatedAt pins the determinism guarantee added with
// the run-manager clock injection: TriggerRun's CreatedAt must come from the
// injected clock, not an inline time.Now(). A regression here would re-open
// the door to non-deterministic scheduling timestamps.
func TestInjectedClockStampsCreatedAt(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	fixed := time.Date(2026, 5, 14, 9, 30, 0, 0, time.UTC)
	jm := NewTaskManager(exec, eb, func() time.Time { return fixed })
	defer jm.Shutdown()

	task := testTask("clocked", model.PolicySkip, 1)
	jm.UpsertTask(task)

	// Block Execute so the run stays pending while we inspect CreatedAt.
	exec.On("Execute", mock.Anything, task, mock.Anything).
		Return(&executor.ExecuteResult{ExitCode: 0}, 50*time.Millisecond)

	run, err := jm.TriggerRun("clocked", model.TriggeredByAPI)
	require.NoError(t, err)
	require.NotNil(t, run)
	assert.True(t, run.CreatedAt.Equal(fixed),
		"CreatedAt must come from the injected clock; got %s, want %s",
		run.CreatedAt, fixed)
}

// TestRemoveTask_DeletesIdleTask covers the simplest reload-remove: a task with
// nothing in flight is dropped from the manager immediately.
func TestRemoveTask_DeletesIdleTask(t *testing.T) {
	jm, _, _ := newTestManager(t)

	jm.UpsertTask(testTask("gone", model.PolicyQueue, 1))
	jm.RemoveTask("gone")

	jm.mu.RLock()
	_, exists := jm.tasks["gone"]
	jm.mu.RUnlock()
	assert.False(t, exists, "an idle removed task must be deleted at once")

	// Removing an unknown task is a no-op.
	jm.RemoveTask("never")
}

// TestRemoveTask_ExitsQueueLoop proves the queue-drain goroutine for a removed
// queue-policy task actually returns: if it leaked, Shutdown (via t.Cleanup)
// would block on the WaitGroup past the deadline. We assert it completes fast.
func TestRemoveTask_ExitsQueueLoop(t *testing.T) {
	jm, _, _ := newTestManager(t)

	jm.UpsertTask(testTask("q", model.PolicyQueue, 1))
	jm.RemoveTask("q")

	done := make(chan struct{})
	go func() {
		jm.Shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown blocked — the removed task's queue loop leaked")
	}
}

// TestRemoveTask_InFlightCronRunFinishes is the crux of the "reload doesn't kill
// running work" guarantee: removing a cron task while a run is in flight must
// keep the taskState alive until that run retires under its original
// definition, then delete it.
func TestRemoveTask_InFlightCronRunFinishes(t *testing.T) {
	jm, exec, eb := newGatedManager(t)

	jm.UpsertTask(testTask("task1", model.PolicySkip, 1))

	done := watchCompletions(eb)
	_, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	require.NoError(t, err)
	exec.WaitStarted(t)

	// Remove while the run is still gated open.
	jm.RemoveTask("task1")

	jm.mu.RLock()
	ts, stillThere := jm.tasks["task1"]
	jm.mu.RUnlock()
	require.True(t, stillThere, "a removed task with an in-flight run must survive until it drains")
	assert.True(t, ts.removed, "the taskState must be latched removed")

	// Let the run finish; it must complete (drains under the old definition),
	// and the taskState must then be deleted by recordRunOutcome.
	exec.ReleaseAll()
	done.waitFor(t, 1)

	assert.Eventually(t, func() bool {
		jm.mu.RLock()
		defer jm.mu.RUnlock()
		_, exists := jm.tasks["task1"]
		return !exists
	}, 2*time.Second, 5*time.Millisecond, "the last draining run must delete the removed task")
}

// TestRemoveTask_StopsServiceInstances verifies a removed service is torn down:
// its instances are cancelled and the supervisor won't refill them.
func TestRemoveTask_StopsServiceInstances(t *testing.T) {
	djm, _, eb := newGatedManager(t)
	jm := TaskManager(djm)

	jm.UpsertTask(serviceTask("svc", 2))

	started := watchRuns(eb, events.EventRunStarted)
	require.NoError(t, jm.StartServiceInstances("svc", model.TriggeredByService))
	started.waitFor(t, 2)

	jm.RemoveTask("svc")

	// Cancelled service instances must not refill, and the task must drain to
	// deletion rather than linger.
	assert.Eventually(t, func() bool {
		djm.mu.RLock()
		defer djm.mu.RUnlock()
		_, exists := djm.tasks["svc"]
		return !exists
	}, 2*time.Second, 5*time.Millisecond, "a removed service must stop and be deleted")
}
