// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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

func TestTriggerRunResolvesAndPersistsParams(t *testing.T) {
	jm, exec, eb := newTestManager(t)

	dest := "/backups"
	task := testTask("task1", model.PolicySkip, 1)
	task.Parameters = []model.TaskParam{
		{Kind: model.ParamArg, Key: "source", Required: true},
		{Kind: model.ParamArg, Key: "dest", Default: &dest},
		{Kind: model.ParamFlag, Key: "--force"},
	}
	jm.UpsertTask(task)

	exec.On("Execute", mock.Anything, mock.Anything, mock.Anything).Return(&executor.ExecuteResult{ExitCode: 0})

	source := "/data"
	done := watchCompletions(eb)
	run, err := jm.TriggerRunWithOptions("task1", TriggerRunOptions{
		TriggeredBy: model.TriggeredByAPI,
		Params:      map[string]*string{"source": &source},
	})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"source":  "/data",
		"dest":    "/backups",
		"--force": "false",
	}, run.Params)

	done.waitFor(t, 1)
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

// TestPolicySkipPublishesTerminalEvent is the regression test for skipped runs
// being invisible to the live UI: the skip persists with end_reason="skipped"
// but, without a terminal event, an SSE subscriber sees the run stuck in
// pending until a page reload re-queries storage. The skip must publish
// EventRunFailed (the same terminal transition queue_full and start_failed
// already rely on) so the UI advances it live. ReasonSkipped is mapped to
// "not a notification" in bridge.go, so this rings no bell.
func TestPolicySkipPublishesTerminalEvent(t *testing.T) {
	jm, exec, eb := newGatedManager(t)

	task := testTask("task1", model.PolicySkip, 1)
	jm.UpsertTask(task)

	// Subscribe before triggering so we never race the publish.
	failed := watchRuns(eb, events.EventRunFailed)

	// First run holds the only slot until cleanup releases it.
	_, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	require.NoError(t, err)
	exec.WaitStarted(t)

	// Overlapping second run is skipped: it must emit a terminal failed event.
	_, err = jm.TriggerRun("task1", model.TriggeredByAPI)
	require.Error(t, err)

	failed.waitFor(t, 1)
	skipped := failed.snapshot()[0]
	require.NotNil(t, skipped.EndReason)
	assert.Equal(t, model.ReasonSkipped, *skipped.EndReason,
		"the terminal event must carry the skip reason so the UI shows it skipped, not failed")
	assert.Equal(t, model.PhaseEnded, skipped.Status)
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

// TestPolicyQueueDropsAtCap exercises the max_queued bound: once the
// pending queue holds max_queued runs, the next firing is recorded with
// end_reason = "queue_full" rather than growing the queue without bound.
func TestPolicyQueueDropsAtCap(t *testing.T) {
	jm, exec, _ := newGatedManager(t)

	task := testTask("task1", model.PolicyQueue, 1)
	task.MaxQueued = 1
	jm.UpsertTask(task)

	first, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	require.NoError(t, err)
	require.Equal(t, model.PhasePending, first.Status)
	// Once first is executing it holds the slot and the queue is empty.
	exec.WaitStarted(t)

	// Second firing occupies the queue (fills max_queued = 1).
	queued, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	require.NoError(t, err)
	require.Equal(t, model.PhasePending, queued.Status)

	// Third firing trips max_queued and is dropped immediately.
	dropped, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	require.Error(t, err, "third firing should be rejected")
	assert.Contains(t, err.Error(), "queue full")
	assert.Equal(t, model.PhaseEnded, dropped.Status)
	require.NotNil(t, dropped.EndReason)
	assert.Equal(t, model.ReasonQueueFull, *dropped.EndReason)
}

func TestPolicyKill(t *testing.T) {
	jm, exec, eb := newGatedManager(t)

	task := testTask("task1", model.PolicyKill, 1)
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

// TestEvaluateConcurrency_QueuePreservesFIFOWhenSlotFree pins the FIFO fix: in
// the window after a slot frees but before the drain loop picks up the queue, a
// fresh trigger must join the back of the queue rather than start ahead of runs
// already waiting.
func TestEvaluateConcurrency_QueuePreservesFIFOWhenSlotFree(t *testing.T) {
	m := &defaultTaskManager{}
	ts := &taskState{
		task:  testTask("t", model.PolicyQueue, 2),
		cond:  sync.NewCond(&sync.Mutex{}),
		queue: []*model.Run{{ID: "queued-1"}}, // one run already waiting
	}

	// A slot is free (0 active < limit 2), but the queue is non-empty.
	action, err := m.evaluateConcurrency(ts, &model.Run{ID: "new"}, 2)
	require.NoError(t, err)
	assert.Equal(t, actionQueued, action, "a free slot must not let a new trigger jump the queue")
	require.Len(t, ts.queue, 2)
	assert.Equal(t, "queued-1", ts.queue[0].ID, "the existing queued run stays at the head")
	assert.Equal(t, "new", ts.queue[1].ID, "the new trigger goes to the back")
}

// TestEvaluateConcurrency_TerminateSkipsAlreadyCancelled pins the terminate fix:
// re-cancelling an already-dying run let the live run set grow past the limit
// under rapid triggers. Each new trigger must cancel a distinct not-yet-cancelled
// victim instead.
func TestEvaluateConcurrency_TerminateSkipsAlreadyCancelled(t *testing.T) {
	m := &defaultTaskManager{}
	var r1cancels, r2cancels int
	r1 := &ActiveRun{Run: &model.Run{ID: "r1"}, Cancel: func() { r1cancels++ }, cancelled: true}
	r2 := &ActiveRun{Run: &model.Run{ID: "r2"}, Cancel: func() { r2cancels++ }}
	ts := &taskState{
		task:   testTask("t", model.PolicyKill, 1),
		active: []*ActiveRun{r1, r2},
	}

	action, err := m.evaluateConcurrency(ts, &model.Run{ID: "r3"}, 1)
	require.NoError(t, err)
	assert.Equal(t, actionStart, action)
	assert.Equal(t, 0, r1cancels, "an already-cancelled run must not be re-cancelled")
	assert.Equal(t, 1, r2cancels, "the live run is terminated to make room for the new one")
	assert.True(t, r2.cancelled, "the terminated run must be latched so a later trigger skips it")
}

// TestGetActiveRunsReturnsRunSnapshot pins that GetActiveRuns hands out a copy of
// each Run, not the live pointer the execute goroutine concurrently mutates.
func TestGetActiveRunsReturnsRunSnapshot(t *testing.T) {
	jm, exec, _ := newGatedManager(t)
	jm.UpsertTask(testTask("task1", model.PolicySkip, 1))
	_, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	require.NoError(t, err)
	exec.WaitStarted(t)
	t.Cleanup(jm.Shutdown)

	jm.mu.RLock()
	live := jm.tasks["task1"].active[0]
	jm.mu.RUnlock()

	snap := jm.GetActiveRuns("task1")
	require.Len(t, snap, 1)
	assert.NotSame(t, live.Run, snap[0].Run, "GetActiveRuns must return a Run copy, not the live pointer")
	assert.Equal(t, live.Run.ID, snap[0].Run.ID, "the copy must carry the same data")
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

// TestReloadDropReAddRevivesQueueTask is the regression test for Bug B: a
// reload that drops a task while a run is still draining, followed by a reload
// that re-adds it, must revive the task rather than leave it latched removed. If
// UpsertTask never cleared the removed flag (and the queue drain didn't restart),
// retiring the old run would delete the now-live task and a newly enqueued run
// would never drain. The test holds run1 in flight across the drop+re-add, then
// enqueues run2 and releases: both must complete and the task must survive.
func TestReloadDropReAddRevivesQueueTask(t *testing.T) {
	jm, exec, eb := newGatedManager(t)

	jm.UpsertTask(testTask("t", model.PolicyQueue, 1))

	// run1 takes the only slot and stays in flight on the gate.
	_, err := jm.TriggerRun("t", model.TriggeredByAPI)
	require.NoError(t, err)
	exec.WaitStarted(t)

	// Reload #1 drops the task (run1 still draining), reload #2 re-adds it.
	jm.RemoveTask("t")
	jm.UpsertTask(testTask("t", model.PolicyQueue, 1))

	// A run enqueued against the revived task; the slot is still held by run1.
	_, err = jm.TriggerRun("t", model.TriggeredByAPI)
	require.NoError(t, err)

	done := watchCompletions(eb)
	exec.ReleaseAll() // run1 retires; the revived task must survive and drain run2

	done.waitFor(t, 2)
	assert.Equal(t, 2, exec.Calls(), "the revived queue task must promote the enqueued run")

	jm.mu.Lock()
	_, ok := jm.tasks["t"]
	jm.mu.Unlock()
	assert.True(t, ok, "retiring the old run must not delete the revived task")
}

// TestUpsertTask_RevivesServiceStoppedOnlyByRemoval guards against a reload
// race: RemoveTask stops a service's supervisor as mechanical bookkeeping
// before its taskState can be deleted, but the taskState (and supervisor)
// survive removal when an instance hasn't retired yet. A reload that re-adds
// the same-named service before that instance retires used to leave the
// revived supervisor permanently stopped — StartServiceInstances silently
// no-ops on a stopped supervisor, so the service came back registered with
// zero live instances and no error surfaced anywhere.
func TestUpsertTask_RevivesServiceStoppedOnlyByRemoval(t *testing.T) {
	jm, exec, eb := newGatedManager(t)
	started := watchRuns(eb, events.EventRunStarted)

	jm.UpsertTask(serviceTask("svc", 1))
	require.NoError(t, jm.StartServiceInstances("svc", model.TriggeredByService))
	started.waitFor(t, 1) // the one instance is live; ts.active is non-empty

	// Reproduce RemoveTask's exact effect on a service whose instance hasn't
	// retired yet, without depending on real cancellation timing — which
	// would race the goroutine that finalizes the cancelled run (and, once
	// ts.active empties, deletes the taskState) against this same test
	// goroutine's next statement.
	jm.mu.Lock()
	ts := jm.tasks["svc"]
	require.NotNil(t, ts)
	ts.removed = true
	ts.supervisor.MarkStopped()
	ts.stoppedByRemoval = true
	jm.mu.Unlock()

	jm.UpsertTask(serviceTask("svc", 1)) // reload #2 re-adds it before the old instance retires

	jm.mu.Lock()
	stillStopped := ts.supervisor.IsStopped()
	jm.mu.Unlock()
	assert.False(t, stillStopped,
		"revival must clear a stop RemoveTask itself set, not one the operator asked for — "+
			"leaving it set makes StartServiceInstances silently no-op forever")

	exec.ReleaseAll()
}

// TestEphemeralTaskReapedAfterRun is the regression test for Bug 7: a
// cloud-inline task is marked Ephemeral and never enters the TOML registry, so
// reconcile can never RemoveTask it. Its taskState (and, for PolicyQueue, its
// drain goroutine) must be reaped once its run retires, or every distinct
// dispatched name leaks a goroutine + state for the daemon's lifetime.
func TestEphemeralTaskReapedAfterRun(t *testing.T) {
	jm, exec, eb := newTestManager(t)

	task := testTask("cloud-adhoc", model.PolicyQueue, 1)
	task.Ephemeral = true
	jm.UpsertTask(task)

	exec.On("Execute", mock.Anything, mock.Anything, mock.Anything).Return(&executor.ExecuteResult{ExitCode: 0})

	done := watchCompletions(eb)
	_, err := jm.TriggerRun("cloud-adhoc", model.TriggeredByCloud)
	require.NoError(t, err)
	done.waitFor(t, 1)

	// The reap runs in retireRun, which may land just after the completion event;
	// poll until the taskState is gone rather than assume synchronous ordering.
	require.Eventually(t, func() bool {
		jm.mu.Lock()
		defer jm.mu.Unlock()
		_, ok := jm.tasks["cloud-adhoc"]
		return !ok
	}, 2*time.Second, 5*time.Millisecond, "an ephemeral task must be reaped after its run retires")
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

// TestShutdownUnblocksParkedDelay is the regression test for a shutdown
// deadlock. A goroutine parked in waitForDelay (retry, restart, or jittered
// dispatch) is tracked by m.wg, so the shutdown drain cannot complete until it
// exits. Before the fix waitForDelay watched persistence.Done(), which is only
// cancelled AFTER the drain — a circular wait that hung shutdown for the full
// remaining delay. The fix watches shutdownCtx, cancelled at the very start of
// ShutdownWithDeadline. A one-hour delay plus a watchdog proves the wait is
// interrupted promptly rather than slept through.
func TestShutdownUnblocksParkedDelay(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now).(*defaultTaskManager)

	// Park a goroutine in waitForDelay, tracked by wg exactly as the real
	// retry/restart/jitter paths track theirs.
	woke := make(chan bool, 1)
	jm.wg.Add(1)
	go func() {
		defer jm.wg.Done()
		woke <- jm.waitForDelay(time.Hour)
	}()

	done := make(chan struct{})
	go func() {
		jm.Shutdown()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown hung: a goroutine parked in waitForDelay did not exit at shutdown start")
	}

	select {
	case got := <-woke:
		assert.False(t, got, "waitForDelay must report false (shutdown) rather than a fired timer")
	case <-time.After(time.Second):
		t.Fatal("waitForDelay never returned")
	}
}

// TestTriggerRunScheduledAtBackdatesCreatedAt pins the jitter mechanism: a run
// dispatched for cron tick T but started at T+offset records CreatedAt = T (the
// tick) and StartedAt = the actual start, so StartedAt − CreatedAt is the jitter
// delay — visible, never hidden. Mirrors RecordMissedRun's backdating.
func TestTriggerRunScheduledAtBackdatesCreatedAt(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	startedAt := time.Date(2026, 6, 10, 3, 7, 0, 0, time.UTC) // tick + 7m offset
	jm := NewTaskManager(exec, eb, func() time.Time { return startedAt })
	t.Cleanup(jm.Shutdown)

	task := testTask("t", model.PolicySkip, 1)
	jm.UpsertTask(task)
	exec.On("Execute", mock.Anything, task, mock.Anything).Return(&executor.ExecuteResult{ExitCode: 0})

	tick := time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC)
	started := watchRuns(eb, events.EventRunStarted)

	run, err := jm.TriggerRunWithOptions("t", TriggerRunOptions{
		TriggeredBy: model.TriggeredByCron,
		ScheduledAt: tick,
	})
	require.NoError(t, err)
	assert.Equal(t, tick, run.CreatedAt, "CreatedAt must be the cron tick, not the clock")

	started.waitFor(t, 1)
	got := started.snapshot()[0]
	require.NotNil(t, got.StartedAt)
	assert.Equal(t, tick, got.CreatedAt)
	assert.Equal(t, startedAt, *got.StartedAt)
	assert.Equal(t, 7*time.Minute, got.StartedAt.Sub(got.CreatedAt),
		"StartedAt − CreatedAt must surface the jitter delay")
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

// TestTerminalEventPublishedOffManagerLock is the regression test for the H2
// re-entrancy deadlock. The three "run never executes" paths — a skip-overlap
// firing (RecordSkippedFiring), a missed-tick firing (RecordMissedRun), and a
// concurrency-policy rejection inside TriggerRunWithOptions — publish a terminal
// EventRunFailed. In the pre-fix code that publish happened while the manager
// still held m.mu (write lock). The cloud bridge subscribes to EventRunFailed
// and re-enters the manager via ServiceSnapshot, which takes m.mu.RLock; an
// RLock requested while the same goroutine already holds the write lock blocks
// forever. The fix defers the terminal publish until after m.mu.Unlock. Each
// subtest wires that exact re-entrant subscriber and a watchdog fails the test
// if the publishing call deadlocks instead of returning.
func TestTerminalEventPublishedOffManagerLock(t *testing.T) {
	// assertReturns runs fn in a goroutine and fails if it does not return
	// within the deadline — the observable symptom of the re-entrancy deadlock.
	assertReturns := func(t *testing.T, name string, fn func()) {
		t.Helper()
		done := make(chan struct{})
		go func() {
			fn()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s deadlocked: the terminal event was published under the manager write lock, so the re-entrant ServiceSnapshot subscriber could not take the read lock", name)
		}
	}

	t.Run("RecordSkippedFiring", func(t *testing.T) {
		jm, _, eb := newTestManager(t)
		jm.UpsertTask(testTask("task1", model.PolicySkip, 1))
		// The cloud bridge: an EventRunFailed handler that reaches back into the
		// manager for a snapshot, re-acquiring m.mu as a reader.
		eb.Subscribe(events.EventRunFailed, func(events.Event) { jm.ServiceSnapshot("task1") })
		assertReturns(t, "RecordSkippedFiring", func() {
			_ = jm.RecordSkippedFiring("task1", model.ReasonSkipped, model.TriggeredByCron)
		})
	})

	t.Run("RecordMissedRun", func(t *testing.T) {
		jm, _, eb := newTestManager(t)
		jm.UpsertTask(testTask("task1", model.PolicyQueue, 1))
		eb.Subscribe(events.EventRunFailed, func(events.Event) { jm.ServiceSnapshot("task1") })
		scheduledAt := time.Date(2026, 6, 9, 3, 0, 0, 0, time.UTC)
		assertReturns(t, "RecordMissedRun", func() {
			_ = jm.RecordMissedRun("task1", scheduledAt, "daemon was down")
		})
	})

	t.Run("TriggerRunWithOptions rejection", func(t *testing.T) {
		jm, exec, eb := newGatedManager(t)
		jm.UpsertTask(testTask("task1", model.PolicySkip, 1))

		// First run holds the only slot so the second firing is skip-rejected,
		// which is the path that publishes the terminal event off the lock.
		_, err := jm.TriggerRun("task1", model.TriggeredByAPI)
		require.NoError(t, err)
		exec.WaitStarted(t)

		// Subscribe only now: the in-flight first run has already emitted its
		// started event and has not failed, so the handler fires solely for the
		// rejection we are about to trigger.
		eb.Subscribe(events.EventRunFailed, func(events.Event) { jm.ServiceSnapshot("task1") })
		assertReturns(t, "TriggerRun (skip rejection)", func() {
			_, _ = jm.TriggerRun("task1", model.TriggeredByAPI)
		})
	})
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

// TestNonServiceRestartBackoffEscalates is the regression test for M6: a
// non-service task with restart=on_failure must escalate its restart backoff
// across consecutive failures, exactly as the supervisor does for services.
// Before the fix RestartAttempt never flowed through TriggerRunOptions →
// ActiveRun → scheduleRestart, so computeRestartDelay saw attempt 0 on every
// restart and the task hot-looped at the flat base delay. With an exponential
// curve the inter-restart gaps must grow (base, 2×base, 4×base); a flat-base
// regression keeps them all ≈ base.
func TestNonServiceRestartBackoffEscalates(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)
	defer jm.Shutdown()

	const base = 40 * time.Millisecond
	task := &model.Task{
		Name:           "task1",
		Kind:           model.KindTask, // non-service: no supervisor to count attempts
		Run:            "exit 1",
		MaxConcurrent:  1,
		OnOverlap:      model.PolicySkip,
		Restart:        model.RestartOnFailure,
		RestartDelay:   base,
		RestartBackoff: model.BackoffExponential,
	}
	jm.UpsertTask(task)

	var mu sync.Mutex
	var starts []time.Time
	exec.On("Execute", mock.Anything, task, mock.Anything).Run(func(mock.Arguments) {
		mu.Lock()
		starts = append(starts, time.Now())
		mu.Unlock()
	}).Return(&executor.ExecuteResult{ExitCode: 1})

	_, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	require.NoError(t, err)

	// Four starts give three inter-restart gaps: base, 2×base, 4×base.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(starts) >= 4
	}, 5*time.Second, 5*time.Millisecond, "restart must keep firing on repeated failure")

	mu.Lock()
	defer mu.Unlock()
	gap1 := starts[1].Sub(starts[0])
	gap2 := starts[2].Sub(starts[1])
	gap3 := starts[3].Sub(starts[2])

	assert.Greater(t, gap2, gap1,
		"second restart must wait longer than the first (backoff escalated): gaps %v, %v", gap1, gap2)
	assert.Greater(t, gap3, gap2,
		"third restart must wait longer than the second (backoff escalated): gaps %v, %v", gap2, gap3)
	// A flat-base regression keeps every gap ≈ base; the escalated third gap
	// (≈ 4×base) must clear that bar with room for scheduling jitter.
	assert.Greater(t, gap3, 2*base,
		"escalated gap must clearly exceed the flat base delay, got %v (base %v)", gap3, base)
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
// longer in the config are dropped as skipped — but must still be persisted with
// a terminal state, never left as a permanent 'pending' row (Prime Directive #1).
func TestLoadPendingRunsSkippedTaskNotFound(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)
	defer jm.Shutdown()

	var mu sync.Mutex
	ended := map[string]model.EndReason{}
	jm.BindPersistenceHook(func(_ context.Context, run *model.Run, _ bool) {
		mu.Lock()
		defer mu.Unlock()
		if run.Status == model.PhaseEnded && run.EndReason != nil {
			ended[run.ID] = *run.EndReason
		}
	})

	pending := []model.Run{
		{ID: "01", TaskName: "ghost", Status: model.PhasePending},
		{ID: "02", TaskName: "ghost", Status: model.PhasePending},
	}
	result := jm.LoadPendingRuns(pending)

	assert.Equal(t, 2, result.Skipped)
	assert.Equal(t, 0, result.Resumed)
	assert.Equal(t, 0, result.Queued)
	assert.Equal(t, 0, result.Failed)

	jm.(*defaultTaskManager).persistence.Flush()
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, model.ReasonSkipped, ended["01"])
	assert.Equal(t, model.ReasonSkipped, ended["02"])
}

// TestRemoveTask_FinalizesQueuedRuns: removing a task on reload must finalize any
// still-queued (persisted 'pending') run instead of dropping the slice and
// leaving a permanent non-terminal row that retention never sweeps.
func TestRemoveTask_FinalizesQueuedRuns(t *testing.T) {
	jm, exec, _ := newGatedManager(t)

	var mu sync.Mutex
	ended := map[string]model.EndReason{}
	jm.BindPersistenceHook(func(_ context.Context, run *model.Run, _ bool) {
		mu.Lock()
		defer mu.Unlock()
		if run.Status == model.PhaseEnded && run.EndReason != nil {
			ended[run.ID] = *run.EndReason
		}
	})

	// Queue-policy task at concurrency 1: first run occupies the slot, second
	// waits in the queue.
	task := testTask("q", model.PolicyQueue, 1)
	jm.UpsertTask(task)

	_, err := jm.TriggerRun("q", model.TriggeredByAPI)
	require.NoError(t, err)
	exec.WaitStarted(t)

	queued, err := jm.TriggerRun("q", model.TriggeredByAPI)
	require.NoError(t, err)

	jm.RemoveTask("q")

	jm.persistence.Flush()
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, model.ReasonSkipped, ended[queued.ID],
		"queued run must be finalized when its task is removed")
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

func TestTerminateRunByExecutionID_NotFound(t *testing.T) {
	jm := NewTaskManager(new(testutil.MockExecutor), events.NewEventBus(), time.Now)
	err := jm.TerminateRunByExecutionID("unknown-ext-id")
	assert.Error(t, err)
}

// TestTerminateRunByExecutionID_MatchesActiveRun exercises the positive
// path: a run triggered with an ExecutionID is cancelled by passing the
// same external ID into TerminateRunByExecutionID.
func TestTerminateRunByExecutionID_MatchesActiveRun(t *testing.T) {
	jm, exec, _ := newGatedManager(t)

	task := testTask("task1", model.PolicySkip, 1)
	jm.UpsertTask(task)

	_, err := jm.TriggerRunWithOptions("task1", TriggerRunOptions{
		TriggeredBy: model.TriggeredByCloud,
		ExecutionID: "ext-123",
	})
	require.NoError(t, err)

	exec.WaitStarted(t) // the run is active and holds its external ID
	require.NoError(t, jm.TerminateRunByExecutionID("ext-123"))
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
// TestListServiceTasks verifies only service tasks are returned (as copies),
// so the cloud integration folds services — not run-to-completion tasks — into
// tasks.sync.
func TestListServiceTasks(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)
	defer jm.Shutdown()

	jm.UpsertTask(&model.Task{
		Name:           "svc",
		Kind:           model.KindService,
		Run:            "echo hi",
		Restart:        model.RestartAlways,
		MaxConcurrent:  1,
		OnOverlap:      model.PolicySkip,
		Instances:      1,
		RestartDelay:   time.Millisecond,
		RestartBackoff: model.BackoffConstant,
	})
	jm.UpsertTask(&model.Task{Name: "plain", Run: "echo x", MaxConcurrent: 1})

	got := jm.ListServiceTasks()
	require.Len(t, got, 1)
	require.Equal(t, "svc", got[0].Name)

	// Returned tasks are copies — mutating one must not affect the manager.
	got[0].Name = "mutated"
	again, ok := jm.GetTask("svc")
	require.True(t, ok)
	require.Equal(t, "svc", again.Name)
}

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

// TestUpsertTask_PolicyChangeAwayFromQueueExitsQueueLoop pins the fix for the
// queue-drain goroutine leak on reload: upserting a task with OnOverlap: queue
// spawns a drain loop; a later reload that flips the same (still-live) task to
// a different policy must stop that loop rather than leave it parked forever
// on cond.Wait(), woken by every retireRun signal but with nothing to drain.
func TestUpsertTask_PolicyChangeAwayFromQueueExitsQueueLoop(t *testing.T) {
	jm, _, _ := newTestManager(t)

	jm.UpsertTask(testTask("task1", model.PolicyQueue, 1))
	jm.mu.RLock()
	require.True(t, jm.tasks["task1"].queueDraining, "the drain loop must be alive under queue policy")
	jm.mu.RUnlock()

	// Reload flips the policy away from queue without removing the task.
	jm.UpsertTask(testTask("task1", model.PolicySkip, 1))

	require.Eventually(t, func() bool {
		jm.mu.RLock()
		defer jm.mu.RUnlock()
		return !jm.tasks["task1"].queueDraining
	}, time.Second, 10*time.Millisecond, "the drain loop must exit once the policy is no longer queue")

	// Confirm it's not just the flag: Shutdown's WaitGroup drain must not block
	// on a leaked goroutine still parked in cond.Wait().
	done := make(chan struct{})
	go func() {
		jm.Shutdown()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown blocked — the queue-drain goroutine leaked after a policy change")
	}
}

// TestUpsertTask_PolicyChangeAwayFromQueueFinalizesQueuedRuns pins the queue-
// stranding bug: a reload that flips a task off queue policy while a run is
// still waiting in ts.queue must finalize that run, exactly like RemoveTask
// does. Before the fix, UpsertTask swapped in the new (non-queue) definition
// and left the queue untouched; queueProcessLoop's own policy re-check would
// just return without ever popping it, leaving the persisted 'pending' row
// stranded forever with nothing to start or end it.
func TestUpsertTask_PolicyChangeAwayFromQueueFinalizesQueuedRuns(t *testing.T) {
	jm, exec, _ := newGatedManager(t)

	var mu sync.Mutex
	ended := map[string]model.EndReason{}
	jm.BindPersistenceHook(func(_ context.Context, run *model.Run, _ bool) {
		mu.Lock()
		defer mu.Unlock()
		if run.Status == model.PhaseEnded && run.EndReason != nil {
			ended[run.ID] = *run.EndReason
		}
	})

	// Queue-policy task at concurrency 1: first run occupies the slot, second
	// waits in the queue.
	task := testTask("q", model.PolicyQueue, 1)
	jm.UpsertTask(task)

	_, err := jm.TriggerRun("q", model.TriggeredByAPI)
	require.NoError(t, err)
	exec.WaitStarted(t)

	queued, err := jm.TriggerRun("q", model.TriggeredByAPI)
	require.NoError(t, err)

	// Reload flips the policy away from queue while the run is still waiting —
	// the active run keeps going under its old definition, but the queued one
	// must not be left behind.
	jm.UpsertTask(testTask("q", model.PolicySkip, 1))

	jm.persistence.Flush()
	mu.Lock()
	gotReason, gotEnded := ended[queued.ID]
	mu.Unlock()
	assert.True(t, gotEnded, "queued run must be finalized when a reload changes the task off queue policy")
	assert.Equal(t, model.ReasonSkipped, gotReason)

	jm.mu.RLock()
	assert.Empty(t, jm.tasks["q"].queue, "the stale queue must be cleared, not left to strand a later upsert")
	jm.mu.RUnlock()
}

// TestScheduleJitteredRun_HeldTaskFireDroppedNotDoubleRun pins the fix for the
// jitter-gate/cron-hold interaction that could double-run a task. A jittered
// fire submitted to the gate can sit pending behind another in-flight jittered
// run; in that window a system cron daemon can come back live and reclaim the
// task (HeldBy set via UpsertTask, exactly what RefreshCronHolds does). The
// stale fire must never start once the gate frees up — a live system cron
// daemon owns this tick now, so RunWisp starting its own run for it would be
// exactly the double-execution the hold exists to prevent.
func TestScheduleJitteredRun_HeldTaskFireDroppedNotDoubleRun(t *testing.T) {
	clk := testutil.NewClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	exec := testutil.NewGateExecutor()
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, clk.Now).(*defaultTaskManager)
	t.Cleanup(jm.Shutdown)

	jm.UpsertTask(testTask("a", model.PolicySkip, 1))
	jm.UpsertTask(testTask("b", model.PolicySkip, 1))

	created := watchRuns(eb, events.EventRunCreated)

	tick := clk.Now()
	// "a" fires immediately (the gate starts free) and occupies it.
	jm.ScheduleJitteredRun("a", tick, tick, time.Hour)
	exec.WaitStarted(t)

	// "b" is submitted with a slot far in the future so the congested gate
	// holds it pending instead of pulling it forward or breaching now.
	jm.ScheduleJitteredRun("b", tick, tick.Add(time.Hour), time.Hour)

	// A system cron daemon reclaims "b" while its fire still sits in the gate —
	// this is exactly what RefreshCronHolds does to the live task set.
	held := testTask("b", model.PolicySkip, 1)
	held.HeldBy = model.HeldByCron
	jm.UpsertTask(held)

	baseline := created.count()

	// Finishing "a" frees the gate, which pulls "b"'s pending fire forward.
	exec.ReleaseAll()

	// Bounded settle window: without the fix, the gate's synchronous
	// advance-on-complete would already have started "b" well within this.
	require.Never(t, func() bool {
		for _, r := range created.snapshot()[baseline:] {
			if r.TaskName == "b" {
				return true
			}
		}
		return false
	}, 300*time.Millisecond, 10*time.Millisecond,
		"a held task's already-queued jitter fire must not start a new run")

	assert.Equal(t, 1, exec.Calls(), "only 'a' should ever have executed")
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

// TestTerminalEventFollowsPersistedTerminalRow is the regression test for the
// exec --json stale-status bug: persistence is async, so publishing a terminal
// event without flushing first let a subscriber that reads storage on the event
// (the SSE streamer's `done` → the exec CLI's run fetch) see a stale
// non-terminal row, and a nil end_reason reads as success — a failed run
// reported exit 0. The hook here writes slowly, standing in for a busy SQLite
// file on a slow disk; it only widens a window that exists on any disk.
func TestTerminalEventFollowsPersistedTerminalRow(t *testing.T) {
	exec := new(testutil.MockExecutor)
	exec.On("Execute", mock.Anything, mock.Anything, mock.Anything).
		Return(&executor.ExecuteResult{ExitCode: 7})

	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)
	defer jm.Shutdown()

	var mu sync.Mutex
	stored := map[string]*model.Run{}
	jm.BindPersistenceHook(func(_ context.Context, run *model.Run, _ bool) {
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		defer mu.Unlock()
		stored[run.ID] = run
	})

	// Read storage from inside the terminal-event handler — exactly what the
	// SSE streamer's `done` makes the exec CLI do.
	observed := make(chan *model.Run, 1)
	eb.Subscribe(events.EventRunFailed, func(e events.Event) {
		re, ok := e.Data.(events.RunEvent)
		if !ok || re.Run == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		observed <- stored[re.Run.ID]
	})

	jm.UpsertTask(testTask("fastfail", model.PolicySkip, 1))
	_, err := jm.TriggerRun("fastfail", model.TriggeredByAPI)
	require.NoError(t, err)

	select {
	case row := <-observed:
		require.NotNil(t, row, "the run must be readable from storage by the time its terminal event fires")
		assert.Equal(t, model.PhaseEnded, row.Status,
			"a terminal event must not outrun the terminal row it announces")
		require.NotNil(t, row.EndReason, "a nil end_reason reads as success and would erase the failure")
		assert.Equal(t, 7, row.ExitCode)
	case <-time.After(3 * time.Second):
		t.Fatal("no terminal event was published")
	}
}
