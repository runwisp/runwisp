// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckNonReloadable_AcceptsTaskOnlyChanges proves the gate ignores the
// reloadable surface: differing [defaults]/tasks must not trip it (the task
// diff handles those), only daemon-wide settings do.
func TestCheckNonReloadable_AcceptsTaskOnlyChanges(t *testing.T) {
	old := &config.Config{Scheduler: config.Scheduler{Timezone: "UTC"}}
	updated := &config.Config{Scheduler: config.Scheduler{Timezone: "UTC"}}
	assert.NoError(t, checkNonReloadable(old, updated))
}

func TestCheckNonReloadable_RejectsEachSection(t *testing.T) {
	base := func() *config.Config {
		return &config.Config{Scheduler: config.Scheduler{Timezone: "UTC"}}
	}

	cases := []struct {
		name   string
		mutate func(*config.Config)
		want   string
	}{
		{"daemon", func(c *config.Config) { c.Daemon.ExternalURL = "https://x" }, "[daemon]"},
		{"timezone", func(c *config.Config) { c.Scheduler.Timezone = "America/New_York" }, "[scheduler] timezone"},
		{"storage", func(c *config.Config) { c.Storage.MaxSize = 1 << 20 }, "[storage]"},
		{"notify", func(c *config.Config) { c.Notify.GlobalNotifiers = []string{"slack"} }, "[notify]"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			updated := base()
			tc.mutate(updated)
			err := checkNonReloadable(base(), updated)
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
	assert.NoError(t, checkNonReloadable(oldCfg, newCfg))
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

// applyDiff runs one reconcile's apply step over the two task sets, with no
// scheduler (the nil checks in apply cover that) and no DB — neither the Changed
// nor the Restamped path touches storage.
func applyDiff(old, updated map[string]*model.Task) (*recordingManager, *TaskRegistry, config.Diff) {
	mgr := &recordingManager{}
	registry := NewTaskRegistry(old)
	r := &Reconciler{registry: registry, manager: mgr}

	diff := config.DiffTasks(old, updated)
	r.apply(diff, old, updated)
	return mgr, registry, diff
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

	mgr, registry, diff := applyDiff(taskSet(staged), taskSet(&promoted))

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

	mgr, registry, diff := applyDiff(taskSet(staged), taskSet(&promoted))

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

	mgr, _, diff := applyDiff(taskSet(before), taskSet(&after))

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

	mgr, _, diff := applyDiff(taskSet(before), taskSet(after))

	require.Len(t, diff.Changed, 1)
	assert.True(t, diff.Changed[0].Has(config.ReasonKind), "task→service is a kind change")
	assert.Equal(t, []string{"web"}, mgr.started, "a newly-serviced task must be started, so the old run drains")
	assert.Empty(t, mgr.recycled, "recycling would cancel the in-flight non-service run")
	assert.Empty(t, mgr.restarted)
	assert.Empty(t, mgr.stopped)
}
