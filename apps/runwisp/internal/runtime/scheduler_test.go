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
	jm := NewTaskManager(exec, eb)

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
	jm := NewTaskManager(exec, eb)

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

func TestSchedulerWarnsOnDSTAmbiguousCron(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb)

	// `0 2 * * *` in America/New_York is the textbook DST footgun: the
	// schedule double-fires every fall-back and is skipped every spring-forward.
	risky := &model.Task{Name: "ny-2am", Cron: "0 2 * * *", Timezone: "America/New_York", Run: "echo"}
	// `0 4 * * *` is outside the DST transition window.
	safe := &model.Task{Name: "ny-4am", Cron: "0 4 * * *", Timezone: "America/New_York", Run: "echo"}
	// UTC has no DST, so even hour 2 is fine.
	utc := &model.Task{Name: "utc-2am", Cron: "0 2 * * *", Timezone: "UTC", Run: "echo"}

	for _, task := range []*model.Task{risky, safe, utc} {
		jm.UpsertTask(task)
	}
	tasks := map[string]*model.Task{"ny-2am": risky, "ny-4am": safe, "utc-2am": utc}

	sched := NewScheduler(jm, tasks, time.UTC)
	res, err := sched.Start()
	assert.NoError(t, err)
	defer sched.Stop()

	combined := ""
	for _, w := range res.Warnings {
		combined += w + "\n"
	}
	assert.Contains(t, combined, "ny-2am",
		"the 02:00 New York task must produce a DST warning")
	assert.NotContains(t, combined, "ny-4am",
		"a 04:00 cron is outside the DST window — no warning expected")
	assert.NotContains(t, combined, "utc-2am",
		"UTC has no DST — no warning expected even at hour 2")
}

func TestSchedulerRejectsBadTaskTimezone(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb)

	task := &model.Task{Name: "bad", Cron: "0 2 * * *", Timezone: "Atlantis/Lost", Run: "echo"}
	jm.UpsertTask(task)
	sched := NewScheduler(jm, map[string]*model.Task{"bad": task}, time.UTC)
	res, err := sched.Start()
	assert.NoError(t, err)
	defer sched.Stop()
	assert.Equal(t, 0, res.Scheduled)
	assert.NotEmpty(t, res.Warnings, "invalid timezone should produce a warning, not a panic")
}
