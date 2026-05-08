// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
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
		Name:        name,
		Parallelism: limit,
		OnOverlap:   policy,
	}
}

func TestUpsertTask(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb)

	task := testTask("task1", model.PolicyQueue, DefaultConcurrencyLimit)
	jm.UpsertTask(task)

	djm := jm.(*defaultTaskManager)
	assert.Contains(t, djm.tasks, "task1")
	assert.NotNil(t, djm.tasks["task1"].cond)
}

func TestTriggerRunBasic(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb)

	task := testTask("task1", model.PolicySkip, 1)
	jm.UpsertTask(task)

	exec.On("Execute", mock.Anything, task, mock.Anything).Return(&executor.ExecuteResult{ExitCode: 0})

	run, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	assert.NoError(t, err)
	assert.NotNil(t, run)

	// Wait for execution to finish (async)
	time.Sleep(50 * time.Millisecond)
	exec.AssertExpectations(t)
}

func TestPolicySkip(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb)

	task := testTask("task1", model.PolicySkip, 1)
	jm.UpsertTask(task)

	// First run blocks
	exec.On("Execute", mock.Anything, task, mock.Anything).Return(&executor.ExecuteResult{ExitCode: 0}, 100*time.Millisecond)

	_, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	assert.NoError(t, err)

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
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb)

	task := testTask("task1", model.PolicyQueue, 1)
	jm.UpsertTask(task)

	// First run blocks
	exec.On("Execute", mock.Anything, task, mock.Anything).Return(&executor.ExecuteResult{ExitCode: 0}, 100*time.Millisecond)

	_, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	assert.NoError(t, err)

	// Second run should queue
	run2, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	assert.NoError(t, err)
	assert.Equal(t, model.PhasePending, run2.Status)

	// Wait for first to finish and second to start
	time.Sleep(500 * time.Millisecond)

	// Both should have executed
	exec.AssertNumberOfCalls(t, "Execute", 2)
}

func TestPolicyTerminate(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb)

	task := testTask("task1", model.PolicyTerminate, 1)
	jm.UpsertTask(task)

	// First run blocks
	exec.On("Execute", mock.Anything, task, mock.Anything).Return(&executor.ExecuteResult{ExitCode: 0}, 200*time.Millisecond)

	_, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	assert.NoError(t, err)

	time.Sleep(10 * time.Millisecond) // Ensure run1 starts

	// Second run should terminate first
	_, err = jm.TriggerRun("task1", model.TriggeredByAPI)
	assert.NoError(t, err)

	time.Sleep(300 * time.Millisecond)

	// Both executed, but first one likely cancelled (mock returns exit code based on context)
	exec.AssertNumberOfCalls(t, "Execute", 2)
}

func TestTerminateRun(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb)

	task := testTask("task1", model.PolicySkip, 1)
	jm.UpsertTask(task)

	exec.On("Execute", mock.Anything, task, mock.Anything).Return(&executor.ExecuteResult{ExitCode: 0}, 200*time.Millisecond)

	run, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	assert.NoError(t, err)

	time.Sleep(10 * time.Millisecond)

	err = jm.TerminateRun(run.ID)
	assert.NoError(t, err)

	time.Sleep(50 * time.Millisecond)
	// Should be done
}

func TestShutdown(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb)

	task := testTask("task1", model.PolicyQueue, 1)
	jm.UpsertTask(task)

	exec.On("Execute", mock.Anything, task, mock.Anything).Return(&executor.ExecuteResult{ExitCode: 0}, 200*time.Millisecond)

	jm.TriggerRun("task1", model.TriggeredByAPI)
	time.Sleep(10 * time.Millisecond)

	jm.Shutdown()
	// Should not panic and cancel runs
}

func TestPersistenceHook(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb)

	var created, updated bool
	jm.BindPersistenceHook(func(run *model.Run, isNew bool) {
		if isNew {
			created = true
		} else {
			updated = true
		}
	})

	task := testTask("task1", model.PolicySkip, 1)
	jm.UpsertTask(task)

	exec.On("Execute", mock.Anything, task, mock.Anything).Return(&executor.ExecuteResult{ExitCode: 0})

	jm.TriggerRun("task1", model.TriggeredByAPI)
	time.Sleep(50 * time.Millisecond)

	assert.True(t, created)
	assert.True(t, updated)
}

// TestRetryFiresOnFailure exercises the retry path end-to-end: a failed
// run must trigger a follow-up run with RetryAttempt incremented and
// RetryOfRunID pointing back at the original.
func TestRetryFiresOnFailure(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb)
	defer jm.Shutdown()

	task := &model.Task{
		Name:          "task1",
		Run:           "echo hi",
		Parallelism:   1,
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
	jm.BindPersistenceHook(func(r *model.Run, isNew bool) {
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
	jm := NewTaskManager(exec, eb)
	defer jm.Shutdown()

	task := &model.Task{
		Name:          "task1",
		Run:           "echo hi",
		Parallelism:   1,
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
	jm := NewTaskManager(exec, eb)
	defer jm.Shutdown()

	task := &model.Task{
		Name:          "task1",
		Run:           "echo hi",
		Parallelism:   1,
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
	jm := NewTaskManager(exec, eb)
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
	jm := NewTaskManager(exec, eb)
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
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb)
	defer jm.Shutdown()

	task := testTask("task1", model.PolicySkip, 1)
	jm.UpsertTask(task)

	exec.On("Execute", mock.Anything, task, mock.Anything).Return(
		&executor.ExecuteResult{ExitCode: 0}, 200*time.Millisecond,
	)

	// Trigger one run that holds the only slot.
	_, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	require.NoError(t, err)
	time.Sleep(10 * time.Millisecond)

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
	jm := NewTaskManager(exec, eb)
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
			got := resolveRunOutcome(&tc.result)
			assert.Equal(t, tc.wantReason, got.endReason)
		})
	}
}

func TestPersistAfterShutdownDoesNotPanic(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb)

	jm.BindPersistenceHook(func(run *model.Run, isNew bool) {})
	jm.Shutdown()

	djm := jm.(*defaultTaskManager)
	assert.NotPanics(t, func() {
		djm.persistence.PersistExisting(&model.Run{})
	})
}
