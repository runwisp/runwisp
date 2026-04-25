// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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

	// Second run should skip
	run2, err := jm.TriggerRun("task1", model.TriggeredByAPI)
	assert.Error(t, err)
	assert.Equal(t, "task already running, skipping (policy: skip)", err.Error())
	assert.Equal(t, model.PhaseEnded, run2.Status)
	assert.Equal(t, model.EndReasonPtr(model.ReasonFailed), run2.EndReason)
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
