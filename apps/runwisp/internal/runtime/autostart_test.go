// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// autostartOffTask is a cron task with autostart = false.
func autostartOffTask(name string) *model.Task {
	task := pausableTask(name)
	task.Autostart = false
	return task
}

// bootScheduler runs the daemon's boot sequence that matters for pauses:
// SnapshotCatchupAnchors, then a fresh scheduler restoring persisted pauses.
func bootScheduler(t *testing.T, db *storage.SQLiteDatabase, tasks map[string]*model.Task, now time.Time) *Scheduler {
	t.Helper()
	_, errs := SnapshotCatchupAnchors(context.Background(), db, tasks, now)
	require.Zero(t, errs)
	sched := NewScheduler(&fakeTaskRunner{}, tasks, time.UTC, func() time.Time { return now })
	_, err := sched.RestorePauses(context.Background(), db)
	require.NoError(t, err)
	return sched
}

// An autostart = false task starts paused on its first boot. Once resumed, a
// restart keeps it resumed: autostart only applies when the task is first
// registered.
func TestBoot_AutostartFalseStartsPausedOnce(t *testing.T) {
	db := newPauseDB(t)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	tasks := taskSet(autostartOffTask("nightly"), pausableTask("daily"))

	first := bootScheduler(t, db, tasks, now)
	require.NotNil(t, first.PausedAt("nightly"))
	assert.True(t, first.PausedAt("nightly").Equal(now))
	assert.False(t, first.IsPaused("daily"), "autostart = true keeps the schedule running")

	require.NoError(t, first.Resume(context.Background(), "nightly"))

	second := bootScheduler(t, db, tasks, now.Add(time.Hour))
	assert.False(t, second.IsPaused("nightly"), "a resumed task stays resumed across restarts")
}

func newAutostartReconciler(t *testing.T, db *storage.SQLiteDatabase, old map[string]*model.Task, now time.Time) (*Reconciler, *Scheduler) {
	t.Helper()
	sched := NewScheduler(&fakeTaskRunner{}, old, time.UTC, func() time.Time { return now })
	_, err := sched.RestorePauses(context.Background(), db)
	require.NoError(t, err)
	_, err = sched.Start()
	require.NoError(t, err)
	t.Cleanup(sched.Stop)
	return &Reconciler{
		registry:  NewTaskRegistry(old),
		manager:   &recordingManager{},
		scheduler: sched,
		db:        db,
		now:       func() time.Time { return now },
	}, sched
}

func assertPersistedPause(t *testing.T, db *storage.SQLiteDatabase, name string) {
	t.Helper()
	paused, err := db.ListPausedTaskSchedules(context.Background())
	require.NoError(t, err)
	assert.Contains(t, paused, name, "the pause must survive a restart")
}

// A reload that adds an autostart = false task pauses it right away, in memory
// and on disk, before its first tick.
func TestReconcile_AddedAutostartFalseTaskStartsPaused(t *testing.T) {
	db := newPauseDB(t)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	old := taskSet()
	r, sched := newAutostartReconciler(t, db, old, now)

	updated := taskSet(autostartOffTask("nightly"), pausableTask("daily"))
	warnings := r.apply(config.DiffTasks(old, updated), old, updated)

	assert.Empty(t, warnings)
	assert.True(t, sched.IsPaused("nightly"))
	assert.Nil(t, sched.GetNextRun("nightly"))
	assertPersistedPause(t, db, "nightly")
	assert.False(t, sched.IsPaused("daily"))
}

// Lifting a cron hold registers the task for the first time, so an
// autostart = false task starts paused instead of firing the moment RunWisp
// takes it over.
func TestReconcile_UnheldAutostartFalseTaskStartsPaused(t *testing.T) {
	db := newPauseDB(t)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	held := autostartOffTask("backup")
	held.HeldBy = model.HeldByCron
	old := taskSet(held)
	r, sched := newAutostartReconciler(t, db, old, now)

	freed := *held
	freed.HeldBy = model.HeldByNothing
	updated := taskSet(&freed)
	r.apply(config.DiffTasks(old, updated), old, updated)

	assert.True(t, sched.IsPaused("backup"))
	assertPersistedPause(t, db, "backup")
}

// Flipping autostart to false on a task RunWisp already registered does not
// pause it (autostart only applies on first registration), so the reload says
// so and names the command that does. An already-paused task needs no nudge.
func TestReconcile_AutostartFlipOnRegisteredTaskWarns(t *testing.T) {
	db := newPauseDB(t)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	before := taskSet(pausableTask("nightly"), pausableTask("quiet"))
	mustRegister(t, db, "nightly", now.Add(-time.Hour))
	mustRegister(t, db, "quiet", now.Add(-time.Hour))
	r, sched := newAutostartReconciler(t, db, before, now)
	require.NoError(t, sched.Pause(context.Background(), "quiet"))

	after := taskSet(autostartOffTask("nightly"), autostartOffTask("quiet"))
	warnings := r.apply(config.DiffTasks(before, after), before, after)

	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], `task "nightly" has autostart = false`)
	assert.Contains(t, warnings[0], "runwisp pause nightly")
	assert.False(t, sched.IsPaused("nightly"), "reload never pauses a task RunWisp already knew")
}

// A task that gains its cron in a reload becomes schedulable then, so that is
// its first registration: an autostart = false task starts paused right away
// instead of firing until the next boot registers it paused.
func TestReconcile_GainedCronAutostartFalseTaskStartsPaused(t *testing.T) {
	db := newPauseDB(t)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	manual := pausableTask("report")
	manual.Cron = ""
	old := taskSet(manual)
	r, sched := newAutostartReconciler(t, db, old, now)

	updated := taskSet(autostartOffTask("report"))
	r.apply(config.DiffTasks(old, updated), old, updated)

	assert.True(t, sched.IsPaused("report"))
	assertPersistedPause(t, db, "report")
}

// A cron-less task added by reload gets no registration, like at boot, so
// gaining a cron later is still its first registration.
func TestReconcile_AddedCronlessTaskThenGainsCronStartsPaused(t *testing.T) {
	db := newPauseDB(t)
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	empty := taskSet()
	r, sched := newAutostartReconciler(t, db, empty, now)

	manual := pausableTask("report")
	manual.Cron = ""
	added := taskSet(manual)
	r.apply(config.DiffTasks(empty, added), empty, added)

	scheduled := taskSet(autostartOffTask("report"))
	r.apply(config.DiffTasks(added, scheduled), added, scheduled)

	assert.True(t, sched.IsPaused("report"))
	assertPersistedPause(t, db, "report")
}
