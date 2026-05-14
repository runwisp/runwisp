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
	"github.com/stretchr/testify/require"
)

func TestScheduler(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)

	task := &model.Task{
		Name: "task1",
		Cron: "@every 1s",
		Run:  "echo hi",
	}
	jm.UpsertTask(task)
	tasks := map[string]*model.Task{"task1": task}

	exec.On("Execute", mock.Anything, task, mock.Anything).Return(&executor.ExecuteResult{ExitCode: 0})

	sched := NewScheduler(jm, tasks, time.UTC)
	_, err := sched.Start()
	assert.NoError(t, err)
	defer sched.Stop()

	// Wait for cron to trigger
	time.Sleep(1500 * time.Millisecond)

	exec.AssertCalled(t, "Execute", mock.Anything, task, mock.Anything)

	next := sched.GetNextRun("task1")
	assert.NotNil(t, next)
}

func TestSchedulerHonorsTaskTimezone(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)

	// New York is UTC-5 in winter, UTC-4 in summer. Either way, 02:00 New York
	// is *not* 02:00 UTC, so a per-task TZ override must produce a different
	// next-run instant than the global UTC scheduler would.
	task := &model.Task{
		Name:     "ny-task",
		Cron:     "0 2 * * *",
		Timezone: "America/New_York",
		Run:      "echo hi",
	}
	jm.UpsertTask(task)
	sched := NewScheduler(jm, map[string]*model.Task{"ny-task": task}, time.UTC)
	_, err := sched.Start()
	assert.NoError(t, err)
	defer sched.Stop()

	next := sched.GetNextRun("ny-task")
	require.NotNil(t, next)
	parsed, err := time.ParseInLocation("2006-01-02 15:04:05", *next, time.UTC)
	require.NoError(t, err)

	loc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	hourInNY := parsed.In(loc).Hour()
	assert.Equal(t, 2, hourInNY,
		"next run must be 02:00 in America/New_York regardless of the daemon's UTC default")
}

func TestSchedulerDSTWallClockDedup(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)

	task := &model.Task{
		Name:     "eu-2am",
		Cron:     "0 2 * * *",
		Timezone: "Europe/Bratislava",
		Run:      "echo",
	}
	jm.UpsertTask(task)
	exec.On("Execute", mock.Anything, task, mock.Anything).Return(&executor.ExecuteResult{ExitCode: 0})

	sched := NewScheduler(jm, map[string]*model.Task{"eu-2am": task}, time.UTC)

	loc, err := time.LoadLocation("Europe/Bratislava")
	require.NoError(t, err)

	// 2026-10-25 is the European fall-back day: at 03:00 CEST clocks rewind
	// to 02:00 CET, so wall-clock 02:00 happens twice. Anchor in UTC to
	// dodge time.Date's documented ambiguity for fold instants:
	//   * UTC 00:00 → 02:00 CEST (first 02:00)
	//   * UTC 01:00 → 02:00 CET  (second 02:00, just after fall-back)
	firstFire := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	secondFire := firstFire.Add(time.Hour)

	require.Equal(t, 2, firstFire.In(loc).Hour(), "anchoring sanity: first fire must read 02:00 in Bratislava")
	require.Equal(t, 2, secondFire.In(loc).Hour(), "anchoring sanity: second fire must also read 02:00 (post fall-back)")

	sched.now = func() time.Time { return firstFire }
	sched.fireOnce("eu-2am", loc)
	sched.now = func() time.Time { return secondFire }
	sched.fireOnce("eu-2am", loc)

	wm := wallMinute{year: 2026, month: time.October, day: 25, hour: 2, minute: 0}
	assert.Equal(t, wm, sched.lastFired["eu-2am"], "lastFired must hold the 02:00 wall-clock minute on the fall-back day")
}

func TestSchedulerDSTDifferentMinuteFires(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)

	task := &model.Task{
		Name:     "eu-mins",
		Cron:     "* * * * *",
		Timezone: "Europe/Bratislava",
		Run:      "echo",
	}
	jm.UpsertTask(task)
	exec.On("Execute", mock.Anything, task, mock.Anything).Return(&executor.ExecuteResult{ExitCode: 0})

	sched := NewScheduler(jm, map[string]*model.Task{"eu-mins": task}, time.UTC)

	loc, err := time.LoadLocation("Europe/Bratislava")
	require.NoError(t, err)

	// Two firings one wall-minute apart must both go through — the dedup
	// only rejects identical (date, hour, minute) tuples.
	first := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	second := first.Add(time.Minute)

	sched.now = func() time.Time { return first }
	sched.fireOnce("eu-mins", loc)
	sched.now = func() time.Time { return second }
	sched.fireOnce("eu-mins", loc)

	wm := wallMinute{year: 2026, month: time.October, day: 25, hour: 2, minute: 1}
	assert.Equal(t, wm, sched.lastFired["eu-mins"], "lastFired must advance when wall-clock minute differs")
}

func TestSchedulerRejectsBadTaskTimezone(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)

	task := &model.Task{Name: "bad", Cron: "0 2 * * *", Timezone: "Atlantis/Lost", Run: "echo"}
	jm.UpsertTask(task)
	sched := NewScheduler(jm, map[string]*model.Task{"bad": task}, time.UTC)
	res, err := sched.Start()
	assert.NoError(t, err)
	defer sched.Stop()
	assert.Equal(t, 0, res.Scheduled)
	assert.NotEmpty(t, res.Warnings, "invalid timezone should produce a warning, not a panic")
}
