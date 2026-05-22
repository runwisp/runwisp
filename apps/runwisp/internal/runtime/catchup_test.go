// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func catchupTask(policy model.MissedRunPolicy) *model.Task {
	return &model.Task{
		Name:           "my-task",
		Cron:           "*/5 * * * *",
		CatchUp:        policy,
		Run:            "echo hi",
		MaxConcurrent:  1,
		OnOverlap:      model.PolicyQueue,
		MaxCatchUpRuns: 100,
	}
}

// mockTaskRunner is a local mock for TaskRunner to avoid circular imports.
type mockTaskRunner struct {
	mock.Mock
}

func (m *mockTaskRunner) TriggerRun(taskName string, triggeredBy model.TriggeredBy) (*model.Run, error) {
	args := m.Called(taskName, triggeredBy)
	return args.Get(0).(*model.Run), args.Error(1)
}

func (m *mockTaskRunner) TriggerRunWithOptions(taskName string, options TriggerRunOptions) (*model.Run, error) {
	args := m.Called(taskName, options)
	return args.Get(0).(*model.Run), args.Error(1)
}

func (m *mockTaskRunner) GetTask(taskName string) (*model.Task, bool) {
	args := m.Called(taskName)
	if args.Get(0) == nil {
		return nil, false
	}
	return args.Get(0).(*model.Task), args.Bool(1)
}

func (m *mockTaskRunner) UpsertTask(task *model.Task) {
	m.Called(task)
}

func (m *mockTaskRunner) TerminateRun(runID string) error {
	return m.Called(runID).Error(0)
}

func (m *mockTaskRunner) TerminateRunByExternalExecutionID(externalExecutionID string) error {
	return m.Called(externalExecutionID).Error(0)
}

func (m *mockTaskRunner) RestartServiceInstances(taskName string) error {
	return m.Called(taskName).Error(0)
}

func (m *mockTaskRunner) StopService(taskName string) error {
	return m.Called(taskName).Error(0)
}

func (m *mockTaskRunner) RecordSkippedFiring(taskName string, reason model.EndReason, triggeredBy model.TriggeredBy) error {
	return m.Called(taskName, reason, triggeredBy).Error(0)
}

func (m *mockTaskRunner) GetActiveRunCount(taskName string) int {
	return m.Called(taskName).Int(0)
}

func TestCountMissedTicks(t *testing.T) {
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

	tests := []struct {
		name     string
		schedule string
		lastRun  time.Time
		now      time.Time
		want     int
	}{
		{
			name:     "no missed ticks",
			schedule: "*/5 * * * *",
			lastRun:  time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC),
			now:      time.Date(2026, 4, 7, 10, 3, 0, 0, time.UTC),
			want:     0,
		},
		{
			name:     "one missed tick",
			schedule: "*/5 * * * *",
			lastRun:  time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC),
			now:      time.Date(2026, 4, 7, 10, 6, 0, 0, time.UTC),
			want:     1,
		},
		{
			name:     "multiple missed ticks",
			schedule: "*/5 * * * *",
			lastRun:  time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC),
			now:      time.Date(2026, 4, 7, 11, 0, 0, 0, time.UTC),
			want:     12,
		},
		{
			name:     "hourly schedule missed for 3 hours",
			schedule: "0 * * * *",
			lastRun:  time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC),
			now:      time.Date(2026, 4, 7, 13, 30, 0, 0, time.UTC),
			want:     3,
		},
		{
			name:     "exactly on next tick",
			schedule: "*/5 * * * *",
			lastRun:  time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC),
			now:      time.Date(2026, 4, 7, 10, 5, 0, 0, time.UTC),
			want:     1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sched, err := parser.Parse(tt.schedule)
			assert.NoError(t, err)
			got := countMissedTicks(sched, tt.lastRun, tt.now)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRunMissedTickCatchUp(t *testing.T) {
	t.Run("policy=latest triggers one run", func(t *testing.T) {
		db := new(testutil.MockRunRepository)
		runner := new(mockTaskRunner)

		now := time.Date(2026, 4, 7, 10, 20, 0, 0, time.UTC) // 4 missed ticks

		lastRun := &model.Run{
			ID:        "last-run",
			TaskName:  "my-task",
			CreatedAt: time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC),
		}

		tasks := map[string]*model.Task{
			"my-task": catchupTask(model.MissedRunLatest),
		}

		db.On("EnsureTaskRegistered", mock.Anything, "my-task", now).Return(nil)
		db.On("GetLastRunByTask", mock.Anything, "my-task").Return(lastRun, nil)
		runner.On("TriggerRun", "my-task", model.TriggeredByCron).Return(&model.Run{}, nil)

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now)

		assert.Equal(t, 1, result.Triggered)
		runner.AssertNumberOfCalls(t, "TriggerRun", 1)
	})

	t.Run("policy=all triggers all missed runs", func(t *testing.T) {
		db := new(testutil.MockRunRepository)
		runner := new(mockTaskRunner)

		now := time.Date(2026, 4, 7, 10, 20, 0, 0, time.UTC) // 4 missed ticks

		lastRun := &model.Run{
			ID:        "last-run",
			TaskName:  "my-task",
			CreatedAt: time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC),
		}

		tasks := map[string]*model.Task{
			"my-task": catchupTask(model.MissedRunAll),
		}

		db.On("EnsureTaskRegistered", mock.Anything, "my-task", now).Return(nil)
		db.On("GetLastRunByTask", mock.Anything, "my-task").Return(lastRun, nil)
		runner.On("TriggerRun", "my-task", model.TriggeredByCron).Return(&model.Run{}, nil)

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now)

		assert.Equal(t, 4, result.Triggered)
		runner.AssertNumberOfCalls(t, "TriggerRun", 4)
	})

	t.Run("policy=skip skips entirely", func(t *testing.T) {
		db := new(testutil.MockRunRepository)
		runner := new(mockTaskRunner)

		tasks := map[string]*model.Task{
			"my-task": catchupTask(model.MissedRunSkip),
		}

		now := time.Date(2026, 4, 7, 10, 20, 0, 0, time.UTC)
		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now)

		assert.Equal(t, 0, result.Triggered)
		db.AssertNotCalled(t, "EnsureTaskRegistered")
		db.AssertNotCalled(t, "GetLastRunByTask")
		runner.AssertNotCalled(t, "TriggerRun")
	})

	t.Run("never-run task first startup has no catch-up", func(t *testing.T) {
		db := new(testutil.MockRunRepository)
		runner := new(mockTaskRunner)

		now := time.Date(2026, 4, 7, 10, 20, 0, 0, time.UTC)

		tasks := map[string]*model.Task{
			"my-task": catchupTask(model.MissedRunLatest),
		}

		// On first startup EnsureTaskRegistered inserts now; GetTaskRegistration
		// returns that same timestamp so countMissedTicks yields zero.
		reg := &model.TaskRegistration{TaskName: "my-task", FirstSeenAt: now}
		db.On("EnsureTaskRegistered", mock.Anything, "my-task", now).Return(nil)
		db.On("GetLastRunByTask", mock.Anything, "my-task").Return(nil, nil)
		db.On("GetTaskRegistration", mock.Anything, "my-task").Return(reg, nil)

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now)

		assert.Equal(t, 0, result.Triggered)
		runner.AssertNotCalled(t, "TriggerRun")
	})

	t.Run("never-run task catches up with policy=latest on subsequent restart", func(t *testing.T) {
		db := new(testutil.MockRunRepository)
		runner := new(mockTaskRunner)

		now := time.Date(2026, 4, 7, 10, 20, 0, 0, time.UTC)
		firstSeen := time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC) // 4 missed ticks ago

		tasks := map[string]*model.Task{
			"my-task": catchupTask(model.MissedRunLatest),
		}

		reg := &model.TaskRegistration{TaskName: "my-task", FirstSeenAt: firstSeen}
		db.On("EnsureTaskRegistered", mock.Anything, "my-task", now).Return(nil)
		db.On("GetLastRunByTask", mock.Anything, "my-task").Return(nil, nil)
		db.On("GetTaskRegistration", mock.Anything, "my-task").Return(reg, nil)
		runner.On("TriggerRun", "my-task", model.TriggeredByCron).Return(&model.Run{}, nil)

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now)

		assert.Equal(t, 1, result.Triggered)
		runner.AssertNumberOfCalls(t, "TriggerRun", 1)
	})

	t.Run("never-run task catches up all missed ticks with policy=all on subsequent restart", func(t *testing.T) {
		db := new(testutil.MockRunRepository)
		runner := new(mockTaskRunner)

		now := time.Date(2026, 4, 7, 10, 20, 0, 0, time.UTC)
		firstSeen := time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC) // 4 missed ticks ago

		tasks := map[string]*model.Task{
			"my-task": catchupTask(model.MissedRunAll),
		}

		reg := &model.TaskRegistration{TaskName: "my-task", FirstSeenAt: firstSeen}
		db.On("EnsureTaskRegistered", mock.Anything, "my-task", now).Return(nil)
		db.On("GetLastRunByTask", mock.Anything, "my-task").Return(nil, nil)
		db.On("GetTaskRegistration", mock.Anything, "my-task").Return(reg, nil)
		runner.On("TriggerRun", "my-task", model.TriggeredByCron).Return(&model.Run{}, nil)

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now)

		assert.Equal(t, 4, result.Triggered)
		runner.AssertNumberOfCalls(t, "TriggerRun", 4)
	})

	t.Run("policy=all caps at max_catch_up_runs", func(t *testing.T) {
		db := new(testutil.MockRunRepository)
		runner := new(mockTaskRunner)

		now := time.Date(2026, 4, 7, 11, 0, 0, 0, time.UTC) // 12 missed ticks at */5
		lastRun := &model.Run{
			ID:        "last-run",
			TaskName:  "my-task",
			CreatedAt: time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC),
		}

		task := catchupTask(model.MissedRunAll)
		task.MaxCatchUpRuns = 5
		tasks := map[string]*model.Task{"my-task": task}

		db.On("EnsureTaskRegistered", mock.Anything, "my-task", now).Return(nil)
		db.On("GetLastRunByTask", mock.Anything, "my-task").Return(lastRun, nil)
		runner.On("TriggerRun", "my-task", model.TriggeredByCron).Return(&model.Run{}, nil)

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now)

		assert.Equal(t, 5, result.Triggered, "cap of 5 must clamp the 12-tick backlog")
		runner.AssertNumberOfCalls(t, "TriggerRun", 5)
	})

	t.Run("policy=all under max_catch_up_runs backfills everything", func(t *testing.T) {
		db := new(testutil.MockRunRepository)
		runner := new(mockTaskRunner)

		now := time.Date(2026, 4, 7, 11, 0, 0, 0, time.UTC) // 12 missed ticks at */5
		lastRun := &model.Run{
			ID:        "last-run",
			TaskName:  "my-task",
			CreatedAt: time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC),
		}

		task := catchupTask(model.MissedRunAll)
		task.MaxCatchUpRuns = 100
		tasks := map[string]*model.Task{"my-task": task}

		db.On("EnsureTaskRegistered", mock.Anything, "my-task", now).Return(nil)
		db.On("GetLastRunByTask", mock.Anything, "my-task").Return(lastRun, nil)
		runner.On("TriggerRun", "my-task", model.TriggeredByCron).Return(&model.Run{}, nil)

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now)

		assert.Equal(t, 12, result.Triggered, "backlog under the cap must be backfilled in full")
		runner.AssertNumberOfCalls(t, "TriggerRun", 12)
	})

	t.Run("no missed ticks no trigger", func(t *testing.T) {
		db := new(testutil.MockRunRepository)
		runner := new(mockTaskRunner)

		now := time.Date(2026, 4, 7, 10, 3, 0, 0, time.UTC) // 0 missed ticks

		lastRun := &model.Run{
			ID:        "last-run",
			TaskName:  "my-task",
			CreatedAt: time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC),
		}

		tasks := map[string]*model.Task{
			"my-task": catchupTask(model.MissedRunLatest),
		}

		db.On("EnsureTaskRegistered", mock.Anything, "my-task", now).Return(nil)
		db.On("GetLastRunByTask", mock.Anything, "my-task").Return(lastRun, nil)

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now)

		assert.Equal(t, 0, result.Triggered)
		runner.AssertNotCalled(t, "TriggerRun")
	})
}
