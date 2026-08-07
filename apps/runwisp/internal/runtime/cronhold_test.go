// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/cronprobe"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// These tests cover the half of the handover the hold gate used to leave to the
// operator. The gate itself (hold_test.go) makes a double fire impossible; what
// this file pins is that the hold does not outlive the cron daemon it was taken
// out for. It used to: the liveness answer was probed once at config load, so
// `systemctl disable --now cron` without a follow-up `runwisp reload` left the
// jobs held by RunWisp and no longer run by cron — nothing fired them at all, and
// with no runs there was no failure record to make that visible.

var cronLive = cronprobe.State{Live: true, State: "is running"}

func anyHold(c CronHoldChange) bool { return len(c.Released) > 0 || len(c.Held) > 0 }

// holdRefreshFixture wires a reconciler over a baseline whose tasks came from a
// crontab, with a real scheduler so a flip is observed where it matters: whether
// a cron entry exists afterwards.
func holdRefreshFixture(t *testing.T, now time.Time, tasks ...*model.Task) (*Reconciler, *Scheduler, *holdCatchupDB) {
	t.Helper()
	live := taskSet(tasks...)
	sched := NewScheduler(&fakeTaskRunner{}, live, time.UTC, nil)
	_, err := sched.Start()
	require.NoError(t, err)
	t.Cleanup(sched.Stop)

	db := newHoldCatchupDB()
	baseline := &config.Config{Tasks: make([]model.Task, 0, len(tasks))}
	for _, task := range tasks {
		baseline.Tasks = append(baseline.Tasks, *task)
	}
	r := &Reconciler{
		registry:  NewTaskRegistry(live),
		manager:   &recordingManager{},
		scheduler: sched,
		db:        db,
		baseline:  baseline,
		now:       func() time.Time { return now },
	}
	return r, sched, db
}

// TestRefreshCronHoldsReleasesWhenCronRetires is the bug. An operator who stops
// cron gets their jobs scheduled without touching RunWisp at all.
func TestRefreshCronHoldsReleasesWhenCronRetires(t *testing.T) {
	now := time.Date(2026, 8, 3, 14, 2, 0, 0, time.UTC)
	r, sched, db := holdRefreshFixture(t, now, heldCronTask("backup", "0 3 * * *"))
	require.Nil(t, sched.GetNextRun("backup"), "held to begin with, or this proves nothing")

	change := r.RefreshCronHolds(cronprobe.State{})

	assert.Equal(t, []string{"backup"}, change.Released)
	assert.Empty(t, change.Held)
	assert.NotNil(t, sched.GetNextRun("backup"),
		"cron is gone, so RunWisp has to be firing this now — nothing else will")
	assert.Equal(t, now, db.registered["backup"],
		"and the catch-up anchor starts at the release, not at some later restart")
}

// TestRefreshCronHoldsStandsDownWhenCronReturns is the mirror. Something brought
// cron back — an operator, a package upgrade unmasking the unit — and RunWisp has
// to stop firing before the next tick fires from both schedulers.
func TestRefreshCronHoldsStandsDownWhenCronReturns(t *testing.T) {
	now := time.Date(2026, 8, 3, 16, 40, 0, 0, time.UTC)
	ours := &model.Task{Name: "backup", Cron: "0 3 * * *", Run: "backup.sh",
		Source: model.SourceCron, SourceFile: "/etc/cron.d/backup"}
	r, sched, db := holdRefreshFixture(t, now, ours)
	require.NotNil(t, sched.GetNextRun("backup"))

	change := r.RefreshCronHolds(cronLive)

	assert.Equal(t, []string{"backup"}, change.Held)
	assert.Empty(t, change.Released)
	assert.Nil(t, sched.GetNextRun("backup"), "cron owns it again; RunWisp must not also fire it")
	assert.NotContains(t, db.registered, "backup",
		"and taking a hold is not the moment RunWisp becomes responsible for the ticks")
}

// TestRefreshCronHoldsLeavesFilesCronDoesNotRead is the per-file half of the gate,
// which the timer must not quietly widen to per-machine. A crontab-format file in
// a path cron never looks at is RunWisp's alone; holding it because some cron
// daemon exists elsewhere on the box would stop those jobs dead for no reason.
func TestRefreshCronHoldsLeavesFilesCronDoesNotRead(t *testing.T) {
	now := time.Date(2026, 8, 3, 16, 40, 0, 0, time.UTC)
	mine := &model.Task{Name: "mine", Cron: "0 3 * * *", Run: "mine.sh",
		Source: model.SourceCron, SourceFile: "/opt/myapp/jobs.cron"}
	r, sched, _ := holdRefreshFixture(t, now, mine)

	assert.False(t, anyHold(r.RefreshCronHolds(cronLive)))
	assert.NotNil(t, sched.GetNextRun("mine"))
}

// TestRefreshCronHoldsLeavesNonCronTasksAlone keeps the timer tied to provenance.
// A task the operator wrote in their own TOML is never cron's, whatever the
// machine says.
func TestRefreshCronHoldsLeavesNonCronTasksAlone(t *testing.T) {
	now := time.Date(2026, 8, 3, 16, 40, 0, 0, time.UTC)
	native := &model.Task{Name: "native", Cron: "0 3 * * *", Run: "mine.sh"}
	r, sched, _ := holdRefreshFixture(t, now, native)

	assert.False(t, anyHold(r.RefreshCronHolds(cronLive)))
	assert.NotNil(t, sched.GetNextRun("native"))
}

// TestRefreshCronHoldsIsIdempotent proves the baseline was actually swapped for
// the re-derived one. If it were not, every subsequent refresh would re-report the
// same flip and churn the scheduler entry of a task nothing had changed about.
func TestRefreshCronHoldsIsIdempotent(t *testing.T) {
	now := time.Date(2026, 8, 3, 14, 2, 0, 0, time.UTC)
	r, sched, _ := holdRefreshFixture(t, now, heldCronTask("backup", "0 3 * * *"))

	require.True(t, anyHold(r.RefreshCronHolds(cronprobe.State{})))
	assert.False(t, anyHold(r.RefreshCronHolds(cronprobe.State{})),
		"the same answer twice is not a second change")
	assert.NotNil(t, sched.GetNextRun("backup"))
}

// TestRefreshCronHoldsSkipsTasksRemovedSinceBaseline covers a reload that dropped
// a task racing a refresh that still has it. Re-registering it would resurrect a
// task the operator deleted.
func TestRefreshCronHoldsSkipsTasksRemovedSinceBaseline(t *testing.T) {
	now := time.Date(2026, 8, 3, 14, 2, 0, 0, time.UTC)
	r, sched, db := holdRefreshFixture(t, now, heldCronTask("backup", "0 3 * * *"))
	r.registry.Delete("backup")

	assert.False(t, anyHold(r.RefreshCronHolds(cronprobe.State{})))
	assert.Nil(t, sched.GetNextRun("backup"))
	assert.NotContains(t, db.registered, "backup")
}

// fakeCronProber scripts the machine's answers for the watcher, so both
// directions of the flip are testable without a real cron daemon, a real
// systemctl, or a real clock.
type fakeCronProber struct {
	answers []cronprobe.State
	calls   int
	applied []cronprobe.State
	change  CronHoldChange
}

func (p *fakeCronProber) probe() cronprobe.State {
	state := p.answers[min(p.calls, len(p.answers)-1)]
	p.calls++
	return state
}

func (p *fakeCronProber) refresh(state cronprobe.State) CronHoldChange {
	p.applied = append(p.applied, state)
	return p.change
}

func newFakeCronWatcher(initial cronprobe.State, answers ...cronprobe.State) (*cronHoldWatcher, *fakeCronProber) {
	prober := &fakeCronProber{answers: answers, change: CronHoldChange{Released: []string{"backup"}}}
	return &cronHoldWatcher{probe: prober.probe, refresh: prober.refresh, last: initial}, prober
}

// TestCronHoldWatcherAppliesOnlyOnChange is the resource-use half. A tick that
// learns nothing must not re-derive holds: doing so would swap every task pointer
// in the registry once a minute, forever, on every box running include_cron.
func TestCronHoldWatcherAppliesOnlyOnChange(t *testing.T) {
	w, prober := newFakeCronWatcher(cronLive, cronLive, cronLive)

	w.tick()
	w.tick()

	assert.Equal(t, 2, prober.calls, "the machine is still asked")
	assert.Empty(t, prober.applied, "but nothing is re-derived from an unchanged answer")
}

// TestCronHoldWatcherAppliesEachFlipOnce covers the loop's state keeping: a flip
// applies once, and the answers after it are the new steady state.
func TestCronHoldWatcherAppliesEachFlipOnce(t *testing.T) {
	dead := cronprobe.State{}
	w, prober := newFakeCronWatcher(cronLive, dead, dead, cronLive, cronLive)

	for range 4 {
		w.tick()
	}

	assert.Equal(t, []cronprobe.State{dead, cronLive}, prober.applied)
}

// TestCronHoldWatcherIgnoresProseOnlyChanges keeps a cron daemon that goes from
// running to enabled-but-stopped from churning the task set. It is live either
// way and owns the same jobs; only the warning wording differs.
func TestCronHoldWatcherIgnoresProseOnlyChanges(t *testing.T) {
	enabled := cronprobe.State{Live: true, State: "is enabled and will start on the next boot"}
	w, prober := newFakeCronWatcher(cronLive, enabled)

	w.tick()

	assert.Empty(t, prober.applied)
}

// TestCronHoldWatcherStartsFromTheLoadedAnswer is why NewCronHoldWatcher takes the
// config's own probe result. Starting from the zero value would make the first
// tick on any box with cron running look like cron had just appeared, and log a
// stand-down for tasks that were held all along.
func TestCronHoldWatcherStartsFromTheLoadedAnswer(t *testing.T) {
	w, prober := newFakeCronWatcher(cronLive, cronLive)

	w.tick()

	assert.Empty(t, prober.applied)
}
