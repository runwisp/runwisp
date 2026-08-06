// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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

	sched := NewScheduler(runner, tasks, time.UTC, nil)
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
	sched := NewScheduler(jm, map[string]*model.Task{"ny-task": task}, time.UTC, nil)
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

	sched := NewScheduler(jm, map[string]*model.Task{"eu-2am": task}, time.UTC, nil)

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
	assert.Equal(t, wm, sched.lastFired["eu-2am"], "lastFired must hold the 02:00 wall-clock instant on the fall-back day")
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

	sched := NewScheduler(jm, map[string]*model.Task{"eu-mins": task}, time.UTC, nil)

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
	assert.Equal(t, wm, sched.lastFired["eu-mins"], "lastFired must advance when wall-clock instant differs")
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
		clock,
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

// TestSchedulerSubMinuteFiresNotSuppressed guards that minute-granular dedup
// does NOT suppress sub-minute firings on a regular (non-DST) day: two firings
// in the same wall-clock minute but at different UTC instants (e.g. */30 firing
// at :00 and :30) each have a distinct UTC timestamp. While their wall-clock
// minute is the same, both are real cron ticks — not DST duplicates — and the
// lastFired tracking only records the latest minute, so the second still fires.
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

	sched := NewScheduler(runner, map[string]*model.Task{"sub-minute": task}, time.UTC, clock)

	for range stamps {
		sched.fireOnce("sub-minute", time.UTC)
	}

	assert.Equal(t, []string{"sub-minute", "sub-minute"}, runner.triggers,
		"firings at :00 and :30 in the same minute must each trigger a run on a non-DST day")
	assert.Empty(t, runner.skips, "sub-minute firings must not be recorded as DST duplicates")
}

// TestSchedulerEverySecondDSTFallbackSuppressed proves the minute-granular dedup
// catches the real DST duplicate: on a fall-back day, two wall-clock minutes
// that repeat fire once and suppress the second.
func TestSchedulerEverySecondDSTFallbackSuppressed(t *testing.T) {
	runner := &fakeTaskRunner{}
	task := &model.Task{
		Name:     "per-minute",
		Cron:     "* * * * *",
		Timezone: "Europe/Bratislava",
		Run:      "echo",
	}

	loc, err := time.LoadLocation("Europe/Bratislava")
	require.NoError(t, err)

	// 2026-10-25 fall-back: wall-clock 02:00 happens twice.
	//   UTC 00:00 → 02:00 CEST (trigger)
	//   UTC 01:00 → 02:00 CET  (duplicate, suppressed)
	stamps := []time.Time{
		time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 10, 25, 1, 0, 0, 0, time.UTC),
	}
	require.Equal(t, 2, stamps[0].In(loc).Hour(), "anchoring sanity: first fire must read 02:xx")
	require.Equal(t, 2, stamps[1].In(loc).Hour(), "anchoring sanity: second fire must read 02:xx (post fall-back)")

	idx := 0
	sched := NewScheduler(runner, map[string]*model.Task{"per-minute": task}, time.UTC, func() time.Time {
		t := stamps[idx]
		idx++
		return t
	})

	for range stamps {
		sched.fireOnce("per-minute", loc)
	}

	assert.Equal(t, []string{"per-minute"}, runner.triggers,
		"the first 02:00 must trigger exactly once")
	assert.Equal(t,
		[]recordedSkip{{taskName: "per-minute", reason: model.ReasonDSTSkipped}},
		runner.skips,
		"the rewound 02:00 must be recorded as dst_skipped")
}

// TestSchedulerWiresDSTGapRecovery proves the scheduler consults a schedule
// that recovers a spring-forward gap tick, rather than robfig's raw one that
// drops it. The cron engine (scheduler.go) and the jitter gap math both parse
// through cronspec.NewScheduleParser; this asserts the schedule the scheduler
// actually stores fires "0 2 * * *" at the 03:00 gap end on the 2024-03-31
// spring-forward day in Europe/Bratislava, where 02:00 never occurs. (The cron
// loop's own clock is robfig-internal and not injectable, so the firing *time*
// — not a live trigger count — is the deterministic contract here.)
func TestSchedulerWiresDSTGapRecovery(t *testing.T) {
	runner := &fakeTaskRunner{}
	loc, err := time.LoadLocation("Europe/Bratislava")
	require.NoError(t, err)

	// A normal day so the jitter plan computes a real window; the schedule it
	// stores is the very one the scheduler consults for every fire.
	now := time.Date(2024, 6, 10, 1, 0, 0, 0, loc)
	task := &model.Task{
		Name:     "nightly",
		Cron:     "0 2 * * *",
		Timezone: "Europe/Bratislava",
		Jitter:   10 * time.Minute,
		Run:      "echo",
	}
	sched := NewScheduler(runner, map[string]*model.Task{"nightly": task}, time.UTC, func() time.Time { return now })
	_, err = sched.Start()
	require.NoError(t, err)
	defer sched.Stop()

	plan, ok := sched.jitterPlans["nightly"]
	require.True(t, ok, "jittered task must have a plan whose schedule the scheduler consults")

	from := time.Date(2024, 3, 31, 0, 30, 0, 0, loc)
	next := plan.schedule.Next(from)
	want := time.Date(2024, 3, 31, 3, 0, 0, 0, loc)
	assert.True(t, want.Equal(next),
		"scheduler's schedule must fire the gap tick at the 03:00 gap end, not drop it: want %s, got %s",
		want, next.In(loc))
}

// jitterTasks returns two identical cron tasks sharing a jitter window. The
// placement levels them to slots {a: 0, b: window}, so "a" takes the earliest
// slot and "b" the far end — a deterministic fixture for the tests below
// without reaching into the placement internals.
func jitterTasks(window time.Duration) map[string]*model.Task {
	mk := func(name string) *model.Task {
		return &model.Task{Name: name, Cron: "0 2 * * *", Jitter: window, Run: "echo hi"}
	}
	return map[string]*model.Task{"a": mk("a"), "b": mk("b")}
}

func TestSchedulerJitterRoutesThroughGate(t *testing.T) {
	runner := &fakeTaskRunner{}
	// 01:00 sits an hour before the 02:00 tick, so the live gap comfortably
	// exceeds the 30m offset and no clamp kicks in.
	now := time.Date(2026, 6, 10, 1, 0, 0, 0, time.UTC)
	tasks := jitterTasks(30 * time.Minute)

	sched := NewScheduler(runner, tasks, time.UTC, func() time.Time { return now })
	_, err := sched.Start()
	require.NoError(t, err)
	defer sched.Stop()

	sched.fireOnce("a", time.UTC)
	sched.fireOnce("b", time.UTC)

	// Every jittered task — including the offset-0 one — is submitted to the
	// gate rather than triggered directly, so the gate can hold peers while one
	// runs. Nothing takes the immediate TriggerRun path.
	assert.Empty(t, runner.triggers, "jittered tasks route through the gate, not direct trigger")

	calls := runner.jitteredCalls()
	require.Len(t, calls, 2)
	byName := map[string]jitteredCall{calls[0].taskName: calls[0], calls[1].taskName: calls[1]}

	// "a" takes the earliest slot (offset 0): its slot deadline is the tick.
	a := byName["a"]
	assert.Equal(t, now, a.tick)
	assert.Equal(t, now, a.slot, "the earliest-slot task's deadline is the tick itself")

	// "b" sits a 30m offset later: slot = tick + 30m, both backdated to the tick.
	b := byName["b"]
	assert.Equal(t, now, b.tick, "the run is backdated to the tick, not the delayed start")
	assert.Equal(t, now.Add(30*time.Minute), b.slot, "b's slot is the tick plus its 30m offset")
	assert.Equal(t, 30*time.Minute, b.window)
}

// TestSchedulerJitterClampsSlotToLiveGap proves the per-fire safety clamp: an
// offset wider than the gap to the next tick is trimmed to just under it, so a
// jittered slot can never land on or past its own next firing.
func TestSchedulerJitterClampsSlotToLiveGap(t *testing.T) {
	runner := &fakeTaskRunner{}
	now := time.Date(2026, 6, 10, 1, 30, 0, 0, time.UTC) // 30m before the 02:00 tick
	// A 2h window is far wider than the 30m gap to the next tick.
	tasks := jitterTasks(2 * time.Hour)

	sched := NewScheduler(runner, tasks, time.UTC, func() time.Time { return now })
	_, err := sched.Start()
	require.NoError(t, err)
	defer sched.Stop()

	sched.fireOnce("b", time.UTC)

	calls := runner.jitteredCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, now.Add(30*time.Minute-time.Second), calls[0].slot,
		"the slot must clamp to one second under the gap to the next tick")
	assert.Equal(t, 30*time.Minute-time.Second, calls[0].window,
		"the window horizon clamps to the live gap too")
}

// TestSchedulerJitterSkipsDSTDuplicate proves a DST wall-clock duplicate is
// recorded as dst_skipped and never jittered, even for a task that carries an
// offset — jitter applies only to genuine firings.
func TestSchedulerJitterSkipsDSTDuplicate(t *testing.T) {
	runner := &fakeTaskRunner{}
	now := time.Date(2026, 6, 10, 1, 0, 0, 0, time.UTC)
	tasks := jitterTasks(30 * time.Minute)

	sched := NewScheduler(runner, tasks, time.UTC, func() time.Time { return now })
	_, err := sched.Start()
	require.NoError(t, err)
	defer sched.Stop()

	sched.fireOnce("b", time.UTC) // genuine firing → jittered
	sched.fireOnce("b", time.UTC) // same wall-minute → DST duplicate

	assert.Len(t, runner.jitteredCalls(), 1, "only the genuine firing is jittered")
	assert.Equal(t,
		[]recordedSkip{{taskName: "b", reason: model.ReasonDSTSkipped}},
		runner.skips,
		"the duplicate must be recorded as dst_skipped, not jittered")
}

// TestSchedulerJitterGetNextRunShowsTick proves the API/TUI/UI see the bare
// cron tick, not tick + slot. Under the gate a task starts at min(when the gate
// frees for it, its slot), so the tick is the earliest and most common actual
// start; surfacing the slot would overstate the delay. Both tasks share a cron,
// so both display the same tick despite carrying different slots.
func TestSchedulerJitterGetNextRunShowsTick(t *testing.T) {
	runner := &fakeTaskRunner{}
	now := time.Date(2026, 6, 10, 1, 0, 0, 0, time.UTC)
	tasks := jitterTasks(30 * time.Minute)

	sched := NewScheduler(runner, tasks, time.UTC, func() time.Time { return now })
	_, err := sched.Start()
	require.NoError(t, err)
	defer sched.Stop()

	nextA := sched.GetNextRun("a")
	nextB := sched.GetNextRun("b")
	require.NotNil(t, nextA)
	require.NotNil(t, nextB)
	assert.Equal(t, *nextA, *nextB,
		"both jittered tasks display the same bare tick, not tick + slot")

	// The displayed instant is the bare cron tick (02:00), with no slot offset
	// folded in. (The cron entry's next-fire is keyed off the real clock, so
	// only the time-of-day is asserted, not the date.)
	parsed, err := time.ParseInLocation("2006-01-02 15:04:05", *nextB, time.UTC)
	require.NoError(t, err)
	assert.Equal(t, 2, parsed.Hour(), "next run is the 02:00 tick, not an offset start")
	assert.Equal(t, 0, parsed.Minute())
}

func TestSchedulerRejectsBadTaskTimezone(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)

	task := &model.Task{Name: "bad", Cron: "0 2 * * *", Timezone: "Atlantis/Lost", Run: "echo"}
	jm.UpsertTask(task)
	sched := NewScheduler(jm, map[string]*model.Task{"bad": task}, time.UTC, nil)
	res, err := sched.Start()
	assert.NoError(t, err)
	defer sched.Stop()
	assert.Equal(t, 0, res.Scheduled)
	assert.NotEmpty(t, res.Warnings, "invalid timezone should produce a warning, not a panic")
}

func TestSchedulerAddTaskAfterStart(t *testing.T) {
	runner := &fakeTaskRunner{}
	sched := NewScheduler(runner, map[string]*model.Task{}, time.UTC, nil)
	_, err := sched.Start()
	require.NoError(t, err)
	defer sched.Stop()

	task := &model.Task{Name: "added", Cron: "@every 1h", Run: "echo hi"}
	require.NoError(t, sched.AddTask(task))

	_, hasEntry := sched.entryIDs["added"]
	assert.True(t, hasEntry, "AddTask must record a cron entry")
	assert.NotNil(t, sched.GetNextRun("added"), "an added task must have a next run")

	// A firing on the freshly added entry triggers the task.
	sched.fireOnce("added", time.UTC)
	assert.Equal(t, 1, runner.triggerCount())
}

func TestSchedulerAddTaskIgnoresNonCron(t *testing.T) {
	runner := &fakeTaskRunner{}
	sched := NewScheduler(runner, map[string]*model.Task{}, time.UTC, nil)
	_, err := sched.Start()
	require.NoError(t, err)
	defer sched.Stop()

	// A service (no cron) must not get a scheduler entry.
	require.NoError(t, sched.AddTask(&model.Task{Name: "svc", Run: "serve"}))
	_, hasEntry := sched.entryIDs["svc"]
	assert.False(t, hasEntry, "a task without a cron expression must not be scheduled")
}

func TestSchedulerRemoveTaskClearsState(t *testing.T) {
	runner := &fakeTaskRunner{}
	task := &model.Task{Name: "tick", Cron: "@every 1h", Run: "echo hi"}
	sched := NewScheduler(runner, map[string]*model.Task{"tick": task}, time.UTC, nil)
	_, err := sched.Start()
	require.NoError(t, err)
	defer sched.Stop()

	// Fire once so lastFired is populated, then remove.
	sched.fireOnce("tick", time.UTC)
	_, hadEntry := sched.entryIDs["tick"]
	require.True(t, hadEntry)
	_, hadFired := sched.lastFired["tick"]
	require.True(t, hadFired)

	sched.RemoveTask("tick")

	_, hasEntry := sched.entryIDs["tick"]
	assert.False(t, hasEntry, "RemoveTask must drop the cron entry")
	_, hasFired := sched.lastFired["tick"]
	assert.False(t, hasFired, "RemoveTask must drop the DST dedup state")

	// Removing again, or a never-scheduled name, is a no-op.
	sched.RemoveTask("tick")
	sched.RemoveTask("never")
}

func TestSchedulerReschedule(t *testing.T) {
	runner := &fakeTaskRunner{}
	task := &model.Task{Name: "tick", Cron: "0 2 * * *", Run: "echo hi"}
	sched := NewScheduler(runner, map[string]*model.Task{"tick": task}, time.UTC, nil)
	_, err := sched.Start()
	require.NoError(t, err)
	defer sched.Stop()

	first := sched.entryIDs["tick"]

	// Change the spec and reschedule; the entry must be rebuilt (new ID).
	task.Cron = "0 3 * * *"
	require.NoError(t, sched.Reschedule(task))

	second, ok := sched.entryIDs["tick"]
	require.True(t, ok)
	assert.NotEqual(t, first, second, "Reschedule must build a fresh cron entry")
}
