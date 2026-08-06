// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"strings"
	"testing"
	"time"
	// Embed the zoneinfo database into the test binary so LoadLocation of a
	// named zone (America/New_York, below) does not depend on the host's system
	// tzdata — keeping the timezone catch-up test hermetic per the repo's
	// unit-test philosophy. cmd/runwisp links this for the real binary; the
	// runtime test binary does not, so it is pinned here.
	_ "time/tzdata"

	"github.com/runwisp/runwisp/internal/cronspec"
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

// TestCatchupScheduleHonorsTaskTimezone verifies catch-up evaluates a task's
// cron in the task's own timezone, not the host's. Before the fix the missed-
// tick recalculation always used the host local zone, mis-detecting the gap for
// any task pinned to a different timezone.
func TestCatchupScheduleHonorsTaskTimezone(t *testing.T) {
	task := &model.Task{Name: "tz", Cron: "0 12 * * *", Timezone: "America/New_York"}

	schedule, loc, err := catchupSchedule(cronspec.NewScheduleParser(), task, time.UTC)
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, "America/New_York", loc.String())

	// January → EST (UTC-5), so the next noon New York after midnight UTC on the
	// 15th is 17:00 UTC that same day. Evaluated in the host zone this would land
	// on a different instant entirely.
	next := schedule.Next(time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC))
	assert.True(t, next.Equal(time.Date(2026, 1, 15, 17, 0, 0, 0, time.UTC)),
		"per-task timezone must anchor the next tick at noon New York (17:00 UTC), got %s", next.UTC())
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

func (m *mockTaskRunner) ScheduleJitteredRun(taskName string, tick, slot time.Time, window time.Duration) {
	m.Called(taskName, tick, slot, window)
}

func (m *mockTaskRunner) GetTask(taskName string) (*model.Task, bool) {
	args := m.Called(taskName)
	if args.Get(0) == nil {
		return nil, false
	}
	return args.Get(0).(*model.Task), args.Bool(1)
}

func (m *mockTaskRunner) ListServiceTasks() []*model.Task { return nil }

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

func (m *mockTaskRunner) RecordMissedRun(taskName string, scheduledAt time.Time, reason string) error {
	return m.Called(taskName, scheduledAt, reason).Error(0)
}

func (m *mockTaskRunner) GetActiveRunCount(taskName string) int {
	return m.Called(taskName).Int(0)
}

func (m *mockTaskRunner) StartServiceInstances(taskName string, triggeredBy model.TriggeredBy) error {
	return m.Called(taskName, triggeredBy).Error(0)
}

func (m *mockTaskRunner) ServiceSnapshot(taskName string) (model.ServiceSnapshot, bool) {
	args := m.Called(taskName)
	return args.Get(0).(model.ServiceSnapshot), args.Bool(1)
}

func TestCountMissedTicks(t *testing.T) {
	parser := cronspec.NewParser()

	tests := []struct {
		name          string
		schedule      string
		lastRun       time.Time
		now           time.Time
		maxCount      int
		want          int
		wantLast      time.Time
		wantTruncated bool
	}{
		{
			name:     "no missed ticks",
			schedule: "*/5 * * * *",
			lastRun:  time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC),
			now:      time.Date(2026, 4, 7, 10, 3, 0, 0, time.UTC),
			maxCount: 1000,
			want:     0,
			wantLast: time.Time{},
		},
		{
			name:     "one missed tick",
			schedule: "*/5 * * * *",
			lastRun:  time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC),
			now:      time.Date(2026, 4, 7, 10, 6, 0, 0, time.UTC),
			maxCount: 1000,
			want:     1,
			wantLast: time.Date(2026, 4, 7, 10, 5, 0, 0, time.UTC),
		},
		{
			name:     "multiple missed ticks",
			schedule: "*/5 * * * *",
			lastRun:  time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC),
			now:      time.Date(2026, 4, 7, 11, 0, 0, 0, time.UTC),
			maxCount: 1000,
			want:     12,
			wantLast: time.Date(2026, 4, 7, 11, 0, 0, 0, time.UTC),
		},
		{
			name:     "hourly schedule missed for 3 hours",
			schedule: "0 * * * *",
			lastRun:  time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC),
			now:      time.Date(2026, 4, 7, 13, 30, 0, 0, time.UTC),
			maxCount: 1000,
			want:     3,
			wantLast: time.Date(2026, 4, 7, 13, 0, 0, 0, time.UTC),
		},
		{
			name:     "exactly on next tick",
			schedule: "*/5 * * * *",
			lastRun:  time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC),
			now:      time.Date(2026, 4, 7, 10, 5, 0, 0, time.UTC),
			maxCount: 1000,
			want:     1,
			wantLast: time.Date(2026, 4, 7, 10, 5, 0, 0, time.UTC),
		},
		{
			// A per-second schedule over an hour is 3600 ticks; counting stops
			// at maxCount and reports truncated so the caller can say "at least
			// N+" instead of walking the whole backlog.
			name:          "per-second backlog truncated at maxCount",
			schedule:      "* * * * * *",
			lastRun:       time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC),
			now:           time.Date(2026, 4, 7, 11, 0, 0, 0, time.UTC),
			maxCount:      5,
			want:          5,
			wantLast:      time.Date(2026, 4, 7, 10, 0, 5, 0, time.UTC),
			wantTruncated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sched, err := parser.Parse(tt.schedule)
			assert.NoError(t, err)
			got, lastTick, truncated := countMissedTicks(sched, tt.lastRun, tt.now, tt.maxCount)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantTruncated, truncated)
			assert.True(t, tt.wantLast.Equal(lastTick),
				"last tick: want %s, got %s", tt.wantLast, lastTick)
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
		runner.On("RecordMissedRun", "my-task", mock.Anything, mock.Anything).Return(nil)

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now, time.UTC)

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
		runner.On("RecordMissedRun", "my-task", mock.Anything, mock.Anything).Return(nil)

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now, time.UTC)

		assert.Equal(t, 4, result.Triggered)
		runner.AssertNumberOfCalls(t, "TriggerRun", 4)
	})

	t.Run("counts missed ticks in the task's own timezone, not the host's", func(t *testing.T) {
		// End-to-end guard for M4: catchupOneTask must evaluate the schedule in
		// the task's timezone before counting missed ticks (the helper-only test
		// covers only that the schedule is *built* in the right zone). A daily
		// midnight cron pinned to New York fires at 05:00 UTC (EST, UTC-5 in
		// January); evaluated in UTC it would fire at 00:00 UTC — a different
		// instant that yields a different missed count over the same window.
		db := new(testutil.MockRunRepository)
		runner := new(mockTaskRunner)

		// Window straddles the offset so the two zones disagree on the count:
		//   New York midnights (05:00 UTC): Jan 15 05:00 and Jan 16 05:00 both
		//     fall in (anchor, now] → 2 missed ticks (the correct answer).
		//   UTC midnights (00:00 UTC): only Jan 16 00:00 falls in the window → 1.
		anchor := time.Date(2026, 1, 15, 3, 0, 0, 0, time.UTC)
		now := time.Date(2026, 1, 16, 6, 0, 0, 0, time.UTC)

		lastRun := &model.Run{ID: "last-run", TaskName: "my-task", CreatedAt: anchor}

		task := catchupTask(model.MissedRunAll)
		task.Cron = "0 0 * * *"
		task.Timezone = "America/New_York"
		tasks := map[string]*model.Task{"my-task": task}

		db.On("EnsureTaskRegistered", mock.Anything, "my-task", now).Return(nil)
		db.On("GetLastRunByTask", mock.Anything, "my-task").Return(lastRun, nil)
		runner.On("TriggerRun", "my-task", model.TriggeredByCron).Return(&model.Run{}, nil)
		runner.On("RecordMissedRun", "my-task", mock.Anything, mock.Anything).Return(nil)

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now, time.UTC)

		assert.Equal(t, 2, result.Triggered,
			"the New York midnights in the window must be counted in America/New_York, not UTC")
		runner.AssertNumberOfCalls(t, "TriggerRun", 2)
	})

	t.Run("policy=skip records the gap but triggers nothing", func(t *testing.T) {
		// Detection is independent of the re-run policy: skip alerts on the
		// miss (one browsable row + notification) while re-firing nothing.
		db := new(testutil.MockRunRepository)
		runner := new(mockTaskRunner)

		now := time.Date(2026, 4, 7, 10, 20, 0, 0, time.UTC) // 4 missed ticks
		lastRun := &model.Run{
			ID:        "last-run",
			TaskName:  "my-task",
			CreatedAt: time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC),
		}
		tasks := map[string]*model.Task{
			"my-task": catchupTask(model.MissedRunSkip),
		}

		db.On("EnsureTaskRegistered", mock.Anything, "my-task", now).Return(nil)
		db.On("GetLastRunByTask", mock.Anything, "my-task").Return(lastRun, nil)
		runner.On("RecordMissedRun", "my-task", mock.Anything, mock.Anything).Return(nil)

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now, time.UTC)

		assert.Equal(t, 0, result.Triggered, "skip never re-fires a missed tick")
		assert.Equal(t, 0, result.Errors)
		runner.AssertNotCalled(t, "TriggerRun")
		runner.AssertNumberOfCalls(t, "RecordMissedRun", 1)
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

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now, time.UTC)

		assert.Equal(t, 0, result.Triggered)
		runner.AssertNotCalled(t, "TriggerRun")
		runner.AssertNotCalled(t, "RecordMissedRun")
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
		runner.On("RecordMissedRun", "my-task", mock.Anything, mock.Anything).Return(nil)

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now, time.UTC)

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
		runner.On("RecordMissedRun", "my-task", mock.Anything, mock.Anything).Return(nil)

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now, time.UTC)

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
		runner.On("RecordMissedRun", "my-task", mock.Anything, mock.Anything).Return(nil)

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now, time.UTC)

		assert.Equal(t, 5, result.Triggered, "cap of 5 must clamp the 12-tick backlog")
		runner.AssertNumberOfCalls(t, "TriggerRun", 5)
	})

	t.Run("per-second backlog is bounded by cap and reported as 'at least'", func(t *testing.T) {
		// A per-second schedule over a 2h outage is 7200 ticks. countMissedTicks
		// stops at the display floor instead of walking all of them, the re-run
		// caps at MaxCatchUpRuns, and the recorded gap is reported honestly as
		// "at least N+" rather than understated.
		db := new(testutil.MockRunRepository)
		runner := new(mockTaskRunner)

		now := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
		lastRun := &model.Run{
			ID:        "last-run",
			TaskName:  "my-task",
			CreatedAt: time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC),
		}

		task := catchupTask(model.MissedRunAll)
		task.Cron = "* * * * * *"
		task.MaxCatchUpRuns = 5
		tasks := map[string]*model.Task{"my-task": task}

		db.On("EnsureTaskRegistered", mock.Anything, "my-task", now).Return(nil)
		db.On("GetLastRunByTask", mock.Anything, "my-task").Return(lastRun, nil)
		runner.On("TriggerRun", "my-task", model.TriggeredByCron).Return(&model.Run{}, nil)
		runner.On("RecordMissedRun", "my-task", mock.Anything, mock.MatchedBy(func(reason string) bool {
			return strings.Contains(reason, "at least") && strings.Contains(reason, "+")
		})).Return(nil)

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now, time.UTC)

		assert.Equal(t, 5, result.Triggered, "cap of 5 must clamp the per-second backlog")
		runner.AssertNumberOfCalls(t, "TriggerRun", 5)
		runner.AssertNumberOfCalls(t, "RecordMissedRun", 1)
	})

	t.Run("truncated backlog anchors the missed row at now, not the capped tick", func(t *testing.T) {
		// When counting truncates, the recorded anchor must be now — not the
		// maxCount-th tick — so a rapid restart doesn't re-count and re-alert the
		// same gap. policy=skip is the exposed case: it triggers no catch-up run
		// whose CreatedAt=now would otherwise advance the anchor.
		db := new(testutil.MockRunRepository)
		runner := new(mockTaskRunner)

		now := time.Date(2026, 4, 7, 12, 0, 0, 0, time.UTC)
		lastRun := &model.Run{
			ID:        "last-run",
			TaskName:  "my-task",
			CreatedAt: time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC), // 7200 per-second ticks
		}

		task := catchupTask(model.MissedRunSkip)
		task.Cron = "* * * * * *"
		tasks := map[string]*model.Task{"my-task": task}

		db.On("EnsureTaskRegistered", mock.Anything, "my-task", now).Return(nil)
		db.On("GetLastRunByTask", mock.Anything, "my-task").Return(lastRun, nil)
		runner.On("RecordMissedRun", "my-task", mock.MatchedBy(func(anchor time.Time) bool {
			return anchor.Equal(now)
		}), mock.Anything).Return(nil)

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now, time.UTC)

		assert.Equal(t, 0, result.Triggered, "skip policy re-runs nothing")
		runner.AssertNumberOfCalls(t, "RecordMissedRun", 1)
		runner.AssertExpectations(t)
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
		runner.On("RecordMissedRun", "my-task", mock.Anything, mock.Anything).Return(nil)

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now, time.UTC)

		assert.Equal(t, 12, result.Triggered, "backlog under the cap must be backfilled in full")
		runner.AssertNumberOfCalls(t, "TriggerRun", 12)
	})

	t.Run("EnsureTaskRegistered error increments errors", func(t *testing.T) {
		db := new(testutil.MockRunRepository)
		runner := new(mockTaskRunner)

		now := time.Date(2026, 4, 7, 10, 20, 0, 0, time.UTC)
		tasks := map[string]*model.Task{
			"my-task": catchupTask(model.MissedRunLatest),
		}

		db.On("EnsureTaskRegistered", mock.Anything, "my-task", now).Return(assert.AnError)

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now, time.UTC)
		assert.Equal(t, 0, result.Triggered)
		assert.Equal(t, 1, result.Errors,
			"failure to register the task surfaces as a catch-up error, not a silent skip")
		runner.AssertNotCalled(t, "TriggerRun")
	})

	t.Run("invalid cron expression is treated as a catch-up error", func(t *testing.T) {
		db := new(testutil.MockRunRepository)
		runner := new(mockTaskRunner)

		now := time.Date(2026, 4, 7, 10, 20, 0, 0, time.UTC)
		// Tasks-with-cron go through validation upstream, but defence-in-depth
		// inside catchup itself shouldn't crash if it ever sees a bad cron.
		task := catchupTask(model.MissedRunLatest)
		task.Cron = "not-a-cron"
		tasks := map[string]*model.Task{"my-task": task}

		db.On("EnsureTaskRegistered", mock.Anything, "my-task", now).Return(nil)

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now, time.UTC)
		assert.Equal(t, 0, result.Triggered)
		assert.Equal(t, 1, result.Errors)
		runner.AssertNotCalled(t, "TriggerRun")
	})

	t.Run("GetLastRunByTask error is surfaced via resolveCatchupAnchor", func(t *testing.T) {
		db := new(testutil.MockRunRepository)
		runner := new(mockTaskRunner)

		now := time.Date(2026, 4, 7, 10, 20, 0, 0, time.UTC)
		tasks := map[string]*model.Task{
			"my-task": catchupTask(model.MissedRunLatest),
		}

		db.On("EnsureTaskRegistered", mock.Anything, "my-task", now).Return(nil)
		db.On("GetLastRunByTask", mock.Anything, "my-task").Return((*model.Run)(nil), assert.AnError)

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now, time.UTC)
		assert.Equal(t, 0, result.Triggered)
		assert.Equal(t, 1, result.Errors)
		runner.AssertNotCalled(t, "TriggerRun")
	})

	t.Run("GetTaskRegistration error is surfaced via resolveCatchupAnchor", func(t *testing.T) {
		db := new(testutil.MockRunRepository)
		runner := new(mockTaskRunner)

		now := time.Date(2026, 4, 7, 10, 20, 0, 0, time.UTC)
		tasks := map[string]*model.Task{
			"my-task": catchupTask(model.MissedRunLatest),
		}

		db.On("EnsureTaskRegistered", mock.Anything, "my-task", now).Return(nil)
		db.On("GetLastRunByTask", mock.Anything, "my-task").Return((*model.Run)(nil), nil)
		db.On("GetTaskRegistration", mock.Anything, "my-task").Return((*model.TaskRegistration)(nil), assert.AnError)

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now, time.UTC)
		assert.Equal(t, 0, result.Triggered)
		assert.Equal(t, 1, result.Errors)
		runner.AssertNotCalled(t, "TriggerRun")
	})

	t.Run("missing registration with no last run skips silently", func(t *testing.T) {
		db := new(testutil.MockRunRepository)
		runner := new(mockTaskRunner)

		now := time.Date(2026, 4, 7, 10, 20, 0, 0, time.UTC)
		tasks := map[string]*model.Task{
			"my-task": catchupTask(model.MissedRunLatest),
		}

		db.On("EnsureTaskRegistered", mock.Anything, "my-task", now).Return(nil)
		db.On("GetLastRunByTask", mock.Anything, "my-task").Return((*model.Run)(nil), nil)
		db.On("GetTaskRegistration", mock.Anything, "my-task").Return((*model.TaskRegistration)(nil), nil)

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now, time.UTC)
		assert.Equal(t, 0, result.Triggered)
		assert.Equal(t, 0, result.Errors,
			"missing registration with no prior run isn't an error — the task simply has no anchor yet")
		runner.AssertNotCalled(t, "TriggerRun")
	})

	t.Run("TriggerRun failure counts as a catch-up error", func(t *testing.T) {
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
		runner.On("RecordMissedRun", "my-task", mock.Anything, mock.Anything).Return(nil)
		runner.On("TriggerRun", "my-task", model.TriggeredByCron).Return((*model.Run)(nil), assert.AnError)

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now, time.UTC)
		assert.Equal(t, 0, result.Triggered)
		assert.Equal(t, 1, result.Errors)
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

		result := RunMissedTickCatchUp(context.Background(), db, tasks, runner, now, time.UTC)

		assert.Equal(t, 0, result.Triggered)
		runner.AssertNotCalled(t, "TriggerRun")
		runner.AssertNotCalled(t, "RecordMissedRun")
	})

	t.Run("records one missed row anchored at the latest tick with the detected count", func(t *testing.T) {
		db := new(testutil.MockRunRepository)
		runner := new(mockTaskRunner)

		now := time.Date(2026, 4, 7, 10, 20, 0, 0, time.UTC) // 4 missed ticks at */5
		lastRun := &model.Run{
			ID:        "last-run",
			TaskName:  "my-task",
			CreatedAt: time.Date(2026, 4, 7, 10, 0, 0, 0, time.UTC),
		}
		tasks := map[string]*model.Task{"my-task": catchupTask(model.MissedRunLatest)}

		db.On("EnsureTaskRegistered", mock.Anything, "my-task", now).Return(nil)
		db.On("GetLastRunByTask", mock.Anything, "my-task").Return(lastRun, nil)
		runner.On("TriggerRun", "my-task", model.TriggeredByCron).Return(&model.Run{}, nil)

		var gotTick time.Time
		var gotReason string
		runner.On("RecordMissedRun", "my-task", mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) {
				gotTick = args.Get(1).(time.Time)
				gotReason = args.Get(2).(string)
			}).Return(nil)

		RunMissedTickCatchUp(context.Background(), db, tasks, runner, now, time.UTC)

		runner.AssertNumberOfCalls(t, "RecordMissedRun", 1)
		wantTick := time.Date(2026, 4, 7, 10, 20, 0, 0, time.UTC)
		assert.True(t, wantTick.Equal(gotTick),
			"scheduledAt is the latest missed tick (the next-boot anchor): want %s, got %s", wantTick, gotTick)
		assert.Contains(t, gotReason, "4 scheduled runs missed")
		assert.Contains(t, gotReason, "since 2026-04-07 10:05")
	})

	t.Run("capped reason reports the full detected count, not the trigger count", func(t *testing.T) {
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

		var gotReason string
		runner.On("RecordMissedRun", "my-task", mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) { gotReason = args.Get(2).(string) }).Return(nil)

		RunMissedTickCatchUp(context.Background(), db, tasks, runner, now, time.UTC)

		assert.Contains(t, gotReason, "12 scheduled runs missed",
			"alert reports the detected total even though only 5 were re-run")
		assert.Contains(t, gotReason, "max_catch_up_runs")
	})
}
