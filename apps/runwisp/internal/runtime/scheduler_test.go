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
	runner := &fakeTaskRunner{}
	task := &model.Task{
		Name: "task1",
		Cron: "@every 1s",
		Run:  "echo hi",
	}
	tasks := map[string]*model.Task{"task1": task}

	sched := NewScheduler(runner, tasks, time.UTC)
	_, err := sched.Start()
	require.NoError(t, err)
	defer sched.Stop()

	// fireOnce is the cron callback; invoking it directly exercises our firing
	// logic without waiting a real second for the @every tick (which would only
	// be testing the cron library).
	sched.fireOnce("task1", time.UTC)
	assert.Equal(t, 1, runner.triggerCount(), "a firing must trigger the task once")

	// Start computes next-run instants synchronously, so this is ready now.
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

	wm := wallSecond{year: 2026, month: time.October, day: 25, hour: 2, minute: 0, second: 0}
	assert.Equal(t, wm, sched.lastFired["eu-2am"], "lastFired must hold the 02:00 wall-clock second on the fall-back day")
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

	wm := wallSecond{year: 2026, month: time.October, day: 25, hour: 2, minute: 1, second: 0}
	assert.Equal(t, wm, sched.lastFired["eu-mins"], "lastFired must advance when wall-clock minute differs")
}

// TestSchedulerFireOnce_GoldenTriggerSkipSequence pins down the firing
// pattern across the 2026-10-25 fall-back in Europe/Bratislava under the
// realistic "0 2 * * *" cron: wall-clock 02:00 happens twice (CEST then
// CET). The dedup must trigger the first instance and skip the second.
// A third firing one wall-minute later must trigger again — proving the
// dedup is per-wall-minute, not a sticky "this hour was already fired".
//
// This is a contract: the (trigger, skip, trigger) sequence is the golden
// outcome; change it only if scheduler semantics intentionally change.
func TestSchedulerFireOnce_GoldenTriggerSkipSequence(t *testing.T) {
	runner := &fakeTaskRunner{}
	task := &model.Task{
		Name:     "tick",
		Cron:     "0 2 * * *",
		Timezone: "Europe/Bratislava",
		Run:      "echo",
	}

	// UTC instants around the 2026-10-25 fall-back, in chronological order:
	//   UTC 00:00 Oct 25 → 02:00 CEST  ← first 02:00 (trigger)
	//   UTC 01:00 Oct 25 → 02:00 CET   ← duplicate 02:00 (skip)
	//   UTC 01:01 Oct 25 → 02:01 CET   ← new wall-minute (trigger)
	stamps := []time.Time{
		time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 25, 1, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 25, 1, 1, 0, 0, time.UTC),
	}
	idx := 0
	clock := func() time.Time {
		t := stamps[idx]
		idx++
		return t
	}

	sched := NewScheduler(
		runner,
		map[string]*model.Task{"tick": task},
		time.UTC,
		WithNow(clock),
	)

	loc, err := time.LoadLocation("Europe/Bratislava")
	require.NoError(t, err)

	for range stamps {
		sched.fireOnce("tick", loc)
	}

	// Observed behavior via the public runner records: two triggers
	// (the unique wall-minutes) and exactly one DST-suppressed skip.
	assert.Equal(t, []string{"tick", "tick"}, runner.triggers,
		"two non-duplicate wall-minutes must each trigger exactly one run")
	assert.Equal(t,
		[]recordedSkip{{taskName: "tick", reason: model.ReasonDSTSkipped}},
		runner.skips,
		"the second 02:00 on the fall-back day must be recorded as dst_skipped")
}

// TestSchedulerSubMinuteFiresNotSuppressed guards the 6-field motivation: two
// firings in the same wall-clock minute but different seconds (e.g. */30 firing
// at :00 and :30) are distinct instants and must both trigger. A minute-granular
// dedup would wrongly suppress the :30 firing as a DST duplicate.
func TestSchedulerSubMinuteFiresNotSuppressed(t *testing.T) {
	runner := &fakeTaskRunner{}
	task := &model.Task{
		Name: "sub-minute",
		Cron: "*/30 * * * * *",
		Run:  "echo",
	}

	stamps := []time.Time{
		time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 6, 1, 12, 0, 30, 0, time.UTC),
	}
	idx := 0
	clock := func() time.Time {
		t := stamps[idx]
		idx++
		return t
	}

	sched := NewScheduler(runner, map[string]*model.Task{"sub-minute": task}, time.UTC, WithNow(clock))

	for range stamps {
		sched.fireOnce("sub-minute", time.UTC)
	}

	assert.Equal(t, []string{"sub-minute", "sub-minute"}, runner.triggers,
		"firings at :00 and :30 in the same minute must each trigger a run")
	assert.Empty(t, runner.skips, "sub-minute firings must not be recorded as DST duplicates")
}

// TestSchedulerEverySecondDSTFallbackSuppressed proves the second-granular dedup
// still catches the real duplicate: under "* * * * * *", a wall-clock second
// that occurs twice on a fall-back day must trigger once and be suppressed once.
func TestSchedulerEverySecondDSTFallbackSuppressed(t *testing.T) {
	runner := &fakeTaskRunner{}
	task := &model.Task{
		Name:     "per-second",
		Cron:     "* * * * * *",
		Timezone: "Europe/Bratislava",
		Run:      "echo",
	}

	loc, err := time.LoadLocation("Europe/Bratislava")
	require.NoError(t, err)

	// 2026-10-25 fall-back: wall-clock 02:00:30 happens twice.
	//   UTC 00:00:30 → 02:00:30 CEST (trigger)
	//   UTC 01:00:30 → 02:00:30 CET  (duplicate, suppressed)
	stamps := []time.Time{
		time.Date(2026, 10, 25, 0, 0, 30, 0, time.UTC),
		time.Date(2026, 10, 25, 1, 0, 30, 0, time.UTC),
	}
	require.Equal(t, 30, stamps[0].In(loc).Second(), "anchoring sanity: first fire must read :30")
	require.Equal(t, 2, stamps[1].In(loc).Hour(), "anchoring sanity: second fire must read 02:xx (post fall-back)")

	idx := 0
	sched := NewScheduler(runner, map[string]*model.Task{"per-second": task}, time.UTC, WithNow(func() time.Time {
		t := stamps[idx]
		idx++
		return t
	}))

	for range stamps {
		sched.fireOnce("per-second", loc)
	}

	assert.Equal(t, []string{"per-second"}, runner.triggers,
		"the first 02:00:30 must trigger exactly once")
	assert.Equal(t,
		[]recordedSkip{{taskName: "per-second", reason: model.ReasonDSTSkipped}},
		runner.skips,
		"the rewound 02:00:30 must be recorded as dst_skipped")
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
