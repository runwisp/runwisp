// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/cronprobe"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckNonReloadable_AcceptsLiveSettings proves the gate lets through
// everything a reload applies live: tasks, [defaults], storage, notifications,
// the timezone and the live [daemon] keys.
func TestCheckNonReloadable_AcceptsLiveSettings(t *testing.T) {
	old := &config.Config{Scheduler: config.Scheduler{Timezone: "UTC"}}
	updated := &config.Config{
		Scheduler: config.Scheduler{Timezone: "Europe/Bratislava"},
		Storage:   config.Storage{MaxSize: 1 << 20, MinFreeSpace: 1 << 10},
		Notify:    config.NotifyConfig{GlobalNotifiers: []string{"slack"}},
		Daemon: config.Daemon{
			ShutdownTimeout: time.Minute,
			ExternalURL:     "https://x",
			CheckUpdates:    true,
			TrustedProxies:  []string{"10.0.0.0/8"},
		},
	}
	assert.NoError(t, CheckNonReloadable(old, updated))
}

func TestCheckNonReloadable_RejectsRestartOnlySettings(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*config.Daemon)
		want   string
	}{
		{"tls", func(d *config.Daemon) { d.TLS = "off" }, "[daemon] tls"},
		{"tls_cert", func(d *config.Daemon) { d.TLSCert = "/c.pem" }, "[daemon] tls"},
		{"tls_key", func(d *config.Daemon) { d.TLSKey = "/k.pem" }, "[daemon] tls"},
		{"metrics_enabled", func(d *config.Daemon) { d.MetricsEnabled = true }, "[daemon] metrics"},
		{"metrics_listen", func(d *config.Daemon) { d.MetricsListen = "127.0.0.1:9478" }, "[daemon] metrics"},
		{"allow_station_dispatch", func(d *config.Daemon) { d.AllowStationDispatch = true }, "[daemon] allow_station_dispatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			updated := &config.Config{}
			tc.mutate(&updated.Daemon)
			err := CheckNonReloadable(&config.Config{}, updated)
			require.Error(t, err, "%s change must be rejected", tc.name)
			assert.Contains(t, err.Error(), tc.want)
			assert.Contains(t, err.Error(), "runwisp restart")
		})
	}
}

// TestCheckNonReloadable_RUNWISPTLSDoesNotFlapReload proves reload survives
// RUNWISP_TLS disagreeing with the TOML: config.Load re-applies the same env
// override on every call (boot and every reconcile), so two configs loaded
// under the same RUNWISP_TLS resolve to the same [daemon].TLS regardless of
// what their TOML says — the reconcile gate must not see that as a change.
func TestCheckNonReloadable_RUNWISPTLSDoesNotFlapReload(t *testing.T) {
	t.Setenv("RUNWISP_TLS", "off")

	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.toml")
	require.NoError(t, os.WriteFile(oldPath, []byte("[daemon]\ntls = \"auto\"\n\n[tasks.t]\nrun = \"/bin/true\"\n"), 0o600))
	newPath := filepath.Join(dir, "new.toml")
	require.NoError(t, os.WriteFile(newPath, []byte("[tasks.t]\nrun = \"/bin/true\"\n"), 0o600))

	oldCfg, err := config.Load(oldPath)
	require.NoError(t, err)
	newCfg, err := config.Load(newPath)
	require.NoError(t, err)

	require.Equal(t, "off", oldCfg.Daemon.TLS)
	require.Equal(t, "off", newCfg.Daemon.TLS)
	assert.NoError(t, CheckNonReloadable(oldCfg, newCfg))
}

// recordingManager is a TaskManager that records the lifecycle calls a reconcile
// makes. The embedded nil interface is deliberate: any method these tests don't
// expect to be called panics loudly instead of silently succeeding.
type recordingManager struct {
	TaskManager
	upserted  []string
	restarted []string
	recycled  []string
	started   []string
	stopped   []string
	removed   []string
	// snapState is the State ServiceSnapshot reports for every service. Empty
	// means "no snapshot" (ok=false); set it to model.ServiceStopped/Running to
	// drive the autostart-flip reload warning.
	snapState string
	location  *time.Location
}

func (m *recordingManager) SetDaemonLocation(loc *time.Location) {
	m.location = loc
}

func (m *recordingManager) UpsertTask(task *model.Task) {
	m.upserted = append(m.upserted, task.Name)
}

func (m *recordingManager) RestartServiceInstances(name string) error {
	m.restarted = append(m.restarted, name)
	return nil
}

func (m *recordingManager) RecycleServiceInstances(name string) error {
	m.recycled = append(m.recycled, name)
	return nil
}

func (m *recordingManager) StartServiceInstances(name string, _ model.TriggeredBy) error {
	m.started = append(m.started, name)
	return nil
}

func (m *recordingManager) StopService(name string) error {
	m.stopped = append(m.stopped, name)
	return nil
}

func (m *recordingManager) RemoveTask(name string) {
	m.removed = append(m.removed, name)
}

func (m *recordingManager) ServiceSnapshot(name string) (model.ServiceSnapshot, bool) {
	if m.snapState == "" {
		return model.ServiceSnapshot{}, false
	}
	return model.ServiceSnapshot{TaskName: name, State: m.snapState}, true
}

// applyDiff runs one reconcile's apply step over the two task sets, with no
// scheduler (the nil checks in apply cover that) and no DB — neither the Changed
// nor the Restamped path touches storage. It returns the non-fatal notices apply
// produced (e.g. an autostart flip reload deliberately doesn't act on).
func applyDiff(old, updated map[string]*model.Task) (*recordingManager, *TaskRegistry, config.Diff, []string) {
	return applyDiffWith(&recordingManager{}, old, updated)
}

// applyDiffWith is applyDiff with a caller-supplied manager, so a test can
// pre-set the service snapshot state the reload warning keys off.
func applyDiffWith(mgr *recordingManager, old, updated map[string]*model.Task) (*recordingManager, *TaskRegistry, config.Diff, []string) {
	registry := NewTaskRegistry(old)
	r := &Reconciler{registry: registry, manager: mgr}

	diff := config.DiffTasks(old, updated)
	warnings := r.apply(diff, old, updated)
	return mgr, registry, diff, warnings
}

// taskSet indexes tasks by name for the diff helpers.
func taskSet(tasks ...*model.Task) map[string]*model.Task {
	out := make(map[string]*model.Task, len(tasks))
	for _, t := range tasks {
		out[t.Name] = t
	}
	return out
}

// TestReconcile_PromotedServiceIsNotRestarted is the regression `runwisp promote`
// needs: promoting moves a definition from the staging file into the operator's
// own config and changes nothing about what runs, so a reload must not recycle a
// running service. Before Staged was masked out of the diff, the flipped flag read
// as a settings change and bounced every promoted service.
func TestReconcile_PromotedServiceIsNotRestarted(t *testing.T) {
	staged := &model.Task{Name: "worker", Kind: model.KindService, Run: "worker --loop", Instances: 2, Source: model.SourceStaged}
	promoted := *staged
	promoted.Source = model.SourceNative

	mgr, registry, diff, _ := applyDiff(taskSet(staged), taskSet(&promoted))

	assert.Empty(t, diff.Changed, "provenance is not a task change")
	assert.Equal(t, []string{"worker"}, diff.Restamped)
	assert.Empty(t, mgr.restarted, "a promoted service must keep running")
	assert.Empty(t, mgr.recycled, "a promoted service must keep running")
	assert.Empty(t, mgr.started)
	assert.Empty(t, mgr.stopped)
	assert.Empty(t, mgr.removed)

	// The live set still picks up the new provenance, so the badge clears.
	assert.Equal(t, []string{"worker"}, mgr.upserted)
	assert.False(t, registry.Snapshot()["worker"].Source == model.SourceStaged)
}

// TestReconcile_PromotedTaskIsNotRescheduled is the cron-task half: a promoted
// task keeps its schedule untouched. A nil scheduler would panic if apply tried to
// reschedule, which is exactly the assertion.
func TestReconcile_PromotedTaskIsNotRescheduled(t *testing.T) {
	staged := &model.Task{Name: "backup", Cron: "0 3 * * *", Run: "backup.sh", Source: model.SourceStaged}
	promoted := *staged
	promoted.Source = model.SourceNative

	mgr, registry, diff, _ := applyDiff(taskSet(staged), taskSet(&promoted))

	assert.True(t, diff.IsEmpty(), "a promote is not a task change the operator needs to see")
	assert.Equal(t, []string{"backup"}, diff.Restamped)
	assert.Equal(t, []string{"backup"}, mgr.upserted)
	assert.False(t, registry.Snapshot()["backup"].Source == model.SourceStaged)
}

// TestReconcile_ChangedServiceIsStillRecycled guards the masking from going too
// far: a genuine definition change must still recycle the service. It must go
// through RecycleServiceInstances, not the operator-restart
// RestartServiceInstances — recycling a changed service on reload must never
// revive one the operator stopped (see manager package tests for that guard).
func TestReconcile_ChangedServiceIsStillRecycled(t *testing.T) {
	before := &model.Task{Name: "worker", Kind: model.KindService, Run: "worker --loop", Source: model.SourceStaged}
	after := *before
	after.Run = "worker --loop --verbose"
	after.Source = model.SourceNative

	mgr, _, diff, _ := applyDiff(taskSet(before), taskSet(&after))

	require.Len(t, diff.Changed, 1)
	assert.True(t, diff.Changed[0].Has(config.ReasonCommand))
	assert.Empty(t, diff.Restamped, "a real change is reported as a change, not a restamp")
	assert.Equal(t, []string{"worker"}, mgr.recycled)
	assert.Empty(t, mgr.restarted, "reload must not use the operator-restart path")
}

// TestReconcile_TaskToServiceKindFlipStartsNotRecycles is the reload-invariant
// regression: when a plain cron/manual task is redefined as a service on reload,
// the run that was in flight under the old non-service definition must finish
// under it. Recycling (RecycleServiceInstances) cancels every active run, so it
// would kill that draining run — a run the supervisor never managed. The flip
// must instead only bring the new service up to its instance count
// (StartServiceInstances), exactly as a freshly added service would.
func TestReconcile_TaskToServiceKindFlipStartsNotRecycles(t *testing.T) {
	before := &model.Task{Name: "web", Cron: "*/5 * * * *", Run: "web --once"}
	after := &model.Task{Name: "web", Kind: model.KindService, Run: "web --loop", Instances: 1, Autostart: true}

	mgr, _, diff, _ := applyDiff(taskSet(before), taskSet(after))

	require.Len(t, diff.Changed, 1)
	assert.True(t, diff.Changed[0].Has(config.ReasonKind), "task→service is a kind change")
	assert.Equal(t, []string{"web"}, mgr.started, "a newly-serviced task must be started, so the old run drains")
	assert.Empty(t, mgr.recycled, "recycling would cancel the in-flight non-service run")
	assert.Empty(t, mgr.restarted)
	assert.Empty(t, mgr.stopped)
}

// TestReconcile_AutostartFlipOnStoppedServiceWarns covers the cutover footgun the
// deployment plan hit: flipping a service's autostart false→true and reloading
// does NOT start it (reload is not a restart), and without a nudge the operator
// is left with a down service and no error anywhere.
func TestReconcile_AutostartFlipOnStoppedServiceWarns(t *testing.T) {
	before := &model.Task{Name: "web", Kind: model.KindService, Run: "web --loop", Instances: 1, Autostart: false}
	after := *before
	after.Autostart = true

	mgr := &recordingManager{snapState: model.ServiceStopped}
	_, _, diff, warnings := applyDiffWith(mgr, taskSet(before), taskSet(&after))

	require.Len(t, diff.Changed, 1, "an autostart flip is a settings change")
	assert.Equal(t, []string{"web"}, mgr.recycled, "a changed service still goes through recycle")
	require.Len(t, warnings, 1, "a stopped service whose autostart flipped on must be flagged")
	assert.Contains(t, warnings[0], "runwisp restart web")
	assert.Contains(t, warnings[0], "autostart=true")
}

// TestReconcile_AutostartFlipOnRunningServiceDoesNotWarn is the negative: a
// service the operator already started by hand is running, so the same autostart
// flip needs no nudge. The warning keys off the live supervisor, not the
// definition delta, so this stays quiet.
func TestReconcile_AutostartFlipOnRunningServiceDoesNotWarn(t *testing.T) {
	before := &model.Task{Name: "web", Kind: model.KindService, Run: "web --loop", Instances: 1, Autostart: false}
	after := *before
	after.Autostart = true

	mgr := &recordingManager{snapState: model.ServiceRunning}
	_, _, _, warnings := applyDiffWith(mgr, taskSet(before), taskSet(&after))

	assert.Empty(t, warnings, "a running service needs no restart nudge")
}

// A reload that locks a paused task with manual_trigger = false clears the pause
// (TOML wins) and reports it in the reload result; a reload that only changes a
// paused task's cron keeps it paused.
func TestReconcile_ClearsPauseTheConfigNoLongerAllows(t *testing.T) {
	locked := &model.Task{Name: "locked", Cron: "0 3 * * *", Run: "a", ManualTrigger: true}
	kept := &model.Task{Name: "kept", Cron: "0 3 * * *", Run: "b", ManualTrigger: true}
	old := taskSet(locked, kept)

	sched := NewScheduler(&fakeTaskRunner{}, old, time.UTC, nil)
	_, err := sched.Start()
	require.NoError(t, err)
	defer sched.Stop()
	require.NoError(t, sched.Pause(context.Background(), "locked"))
	require.NoError(t, sched.Pause(context.Background(), "kept"))

	lockedNow := *locked
	lockedNow.ManualTrigger = false
	keptNow := *kept
	keptNow.Cron = "0 4 * * *"
	updated := taskSet(&lockedNow, &keptNow)

	r := &Reconciler{registry: NewTaskRegistry(old), manager: &recordingManager{}, scheduler: sched}
	warnings := r.apply(config.DiffTasks(old, updated), old, updated)

	assert.Contains(t, warnings, `task "locked": schedule pause cleared (manual_trigger = false)`)
	assert.Nil(t, sched.PausedAt("locked"))
	assert.NotNil(t, sched.PausedAt("kept"), "a cron change keeps the pause")
	assert.Nil(t, sched.GetNextRun("kept"))
}

// TestReconcile_SettingsHookGatesTimezoneAndCommit proves the settings hook is
// part of validate-first: when it fails, the reload is rejected and nothing
// moves (the schedule keeps its zone, the commit never runs). When it passes, a
// timezone change re-bases the schedule and the manager's health check zone,
// the hook's keys are reported after the timezone, and the commit runs before
// the task set changes, so what the reload starts runs under the new settings.
func TestReconcile_SettingsHookGatesTimezoneAndCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runwisp.toml")
	write := func(tz, run string) {
		body := "[daemon]\ntimezone = \"" + tz + "\"\n\n[tasks.t]\nrun = \"" + run + "\"\ncron = \"0 3 * * *\"\n"
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	}
	write("UTC", "/bin/true")
	base, err := config.Load(path)
	require.NoError(t, err)

	tasks := tasksByName(base)
	sched := NewScheduler(&fakeTaskRunner{}, tasks, time.UTC, nil)
	_, err = sched.Start()
	require.NoError(t, err)
	defer sched.Stop()

	hourOfNextRun := func() int {
		var hour int
		require.Eventually(t, func() bool {
			next := sched.GetNextRun("t")
			if next == nil || next.IsZero() {
				return false
			}
			hour = next.UTC().Hour()
			return true
		}, time.Second, 5*time.Millisecond)
		return hour
	}
	require.Equal(t, 3, hourOfNextRun())

	hookErr := errors.New("bad notifier")
	committed, committedBeforeApply := false, false
	mgr := &recordingManager{}
	r := NewReconciler(ReconcilerDeps{
		ConfigPath: path,
		Baseline:   base,
		Registry:   NewTaskRegistry(tasks),
		Scheduler:  sched,
		Manager:    mgr,
		Snapshot:   config.NewSnapshot(path, base, time.Now()),
		Now:        time.Now,
		Settings: func(_, _ *config.Config) ([]string, func(), error) {
			if hookErr != nil {
				return nil, nil, hookErr
			}
			return []string{"storage.max_size"}, func() {
				committed = true
				committedBeforeApply = len(mgr.upserted) == 0
			}, nil
		},
	})

	write("Asia/Tokyo", "/bin/false") // UTC+9, no DST: 03:00 Tokyo is 18:00 UTC
	_, err = r.Reconcile()
	require.ErrorContains(t, err, "bad notifier")
	assert.False(t, committed)
	assert.Nil(t, mgr.location)
	assert.Equal(t, 3, hourOfNextRun(), "a rejected reload must not move the schedule")

	hookErr = nil
	result, err := r.Reconcile()
	require.NoError(t, err)
	assert.True(t, committed)
	assert.True(t, committedBeforeApply, "settings must be live before the changed task is swapped in")
	assert.Equal(t, []string{"t"}, mgr.upserted)
	assert.Equal(t, []string{"daemon.timezone", "storage.max_size"}, result.Settings)
	require.NotNil(t, mgr.location)
	assert.Equal(t, "Asia/Tokyo", mgr.location.String(), "health checks follow the new zone")
	assert.Equal(t, 18, hourOfNextRun())
}

// TestReconcile_UnsetTimezoneKeepsBootZone proves a reload doesn't pick up a
// host zone change on its own: with [daemon] timezone unset, the zone the
// daemon booted with stays until a restart, however the host zone moved.
func TestReconcile_UnsetTimezoneKeepsBootZone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runwisp.toml")
	require.NoError(t, os.WriteFile(path, []byte("[tasks.t]\nrun = \"/bin/true\"\ncron = \"0 3 * * *\"\n"), 0o600))
	t.Setenv("TZ", "UTC")
	base, err := config.Load(path)
	require.NoError(t, err)
	require.Equal(t, config.TimezoneSourceSystem, base.Scheduler.Source)

	mgr := &recordingManager{}
	r := NewReconciler(ReconcilerDeps{
		ConfigPath: path,
		Baseline:   base,
		Registry:   NewTaskRegistry(tasksByName(base)),
		Manager:    mgr,
		Snapshot:   config.NewSnapshot(path, base, time.Now()),
		Now:        time.Now,
	})

	t.Setenv("TZ", "Asia/Tokyo") // the host zone moves under the daemon
	result, err := r.Reconcile()
	require.NoError(t, err)
	assert.Empty(t, result.Settings)
	assert.Nil(t, mgr.location)
}

// TestReconcile_CronHoldWatcherFollowsIncludeCron proves the cron-hold watcher
// tracks the live config, not the boot one: a reload that adds include_cron
// starts it, a reload that drops the last crontab stops it, a reload that keeps
// it reseeds it, and the stop func WatchCronHolds returns shuts it down for good.
func TestReconcile_CronHoldWatcherFollowsIncludeCron(t *testing.T) {
	dir := t.TempDir()
	// include_cron refuses crontabs under a group/world-writable directory, and
	// t.TempDir follows the umask.
	require.NoError(t, os.Chmod(dir, 0o755))
	path := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.Mkdir(filepath.Join(dir, "crontabs"), 0o755))
	require.NoError(t, os.Chmod(filepath.Join(dir, "crontabs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "crontabs", "backup"),
		[]byte("0 3 * * * /usr/local/bin/backup.sh\n"), 0o600))
	plain := "[tasks.t]\nrun = \"/bin/true\"\n"
	withCron := "[daemon]\ninclude_cron = [\"crontabs/*\"]\n\n" + plain
	write := func(body string) { require.NoError(t, os.WriteFile(path, []byte(body), 0o600)) }

	write(plain)
	base, err := config.Load(path)
	require.NoError(t, err)
	r := NewReconciler(ReconcilerDeps{
		ConfigPath: path,
		Baseline:   base,
		Registry:   NewTaskRegistry(tasksByName(base)),
		Manager:    &recordingManager{},
		DB:         newHoldCatchupDB(),
		Snapshot:   config.NewSnapshot(path, base, time.Now()),
		Now:        time.Now,
	})
	watching := func() bool {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.stopCronWatcher != nil
	}

	stop := r.WatchCronHolds(func() cronprobe.State { return cronprobe.State{} })
	assert.False(t, watching(), "no crontab, no watcher")

	write(withCron)
	_, err = r.Reconcile()
	require.NoError(t, err)
	assert.True(t, watching(), "a reload adding include_cron must start the watcher")

	write(plain)
	_, err = r.Reconcile()
	require.NoError(t, err)
	assert.False(t, watching(), "a reload dropping the last crontab must stop the watcher")

	write(withCron)
	_, err = r.Reconcile()
	require.NoError(t, err)

	// A reload re-probes cron, so the running watcher must be reseeded with that
	// answer. One that kept its old remembered liveness would miss cron flipping
	// back to it and never re-hold the tasks.
	r.mu.Lock()
	replaced, prev := false, r.stopCronWatcher
	r.stopCronWatcher = func() { replaced = true; prev() }
	r.mu.Unlock()
	_, err = r.Reconcile()
	require.NoError(t, err)
	assert.True(t, replaced, "a reload must replace the running watcher")
	assert.True(t, watching())

	stop()
	assert.False(t, watching(), "stop must shut the watcher down")
}

// TestReconcile_RemovedTaskStartsFreshWhenReAdded is the reload half of the
// removed-task regression (see TestSnapshotCatchupAnchors_RemovedTaskStartsFresh):
// removing a task by reload forgets its registration, so adding it back by a
// later reload anchors catch-up at that reload, not at the task's old history.
func TestReconcile_RemovedTaskStartsFreshWhenReAdded(t *testing.T) {
	ctx := context.Background()
	db, err := storage.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	task := &model.Task{Name: "nightly", Cron: "0 3 * * *", Run: "a"}
	old := taskSet(task)
	firstSeen := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, db.EnsureTaskRegistered(ctx, task.Name, firstSeen))
	require.NoError(t, db.CreateRun(ctx, &model.Run{
		ID: "run-1", TaskName: task.Name, Status: model.PhaseEnded,
		TriggeredBy: model.TriggeredByCron, CreatedAt: firstSeen.Add(time.Hour),
	}))

	now := firstSeen.Add(2 * time.Hour)
	r := &Reconciler{registry: NewTaskRegistry(old), manager: &recordingManager{}, db: db, now: func() time.Time { return now }}
	none := taskSet()
	r.apply(config.DiffTasks(old, none), old, none)

	now = firstSeen.AddDate(0, 1, 0)
	back := taskSet(task) // the registry deleted from old
	r.apply(config.DiffTasks(none, back), none, back)

	reg, err := db.GetTaskRegistration(ctx, task.Name)
	require.NoError(t, err)
	require.NotNil(t, reg)
	assert.True(t, now.Equal(reg.FirstSeenAt), "first_seen_at = %v, want the re-adding reload %v", reg.FirstSeenAt, now)
	assert.Nil(t, reg.LastRunAt)
}
