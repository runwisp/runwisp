// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage"
)

// Reconciler applies an explicit `runwisp reload` (CLI over the local socket or
// SIGHUP) to the running daemon. It is validate-first and atomic at the config
// layer: the whole runwisp.toml is re-loaded and re-validated, and any change
// to a non-reloadable setting is rejected, before a single live task is
// touched. On rejection nothing is applied and the running set is unchanged.
//
// Reload never re-runs boot-only behaviour: added tasks get no catch-up and no
// run_on_start firing. The reconciler owns one mutex so a CLI reload and a
// SIGHUP can't interleave.
type Reconciler struct {
	configPath string
	registry   *TaskRegistry
	scheduler  *Scheduler
	manager    TaskManager
	db         storage.RunRepository
	snapshot   *config.Snapshot
	now        func() time.Time

	mu       sync.Mutex // serialises reloads
	baseline *config.Config
}

// ReconcilerDeps wires a Reconciler. Baseline is the config the daemon booted
// with (the current live set); the reconciler replaces it after each successful
// reload. Snapshot is re-pinned on success so config_stale reflects the applied
// config. Now may be nil — NewReconciler defaults it to time.Now.
type ReconcilerDeps struct {
	ConfigPath string
	Baseline   *config.Config
	Registry   *TaskRegistry
	Scheduler  *Scheduler
	Manager    TaskManager
	DB         storage.RunRepository
	Snapshot   *config.Snapshot
	Now        func() time.Time
}

// NewReconciler wires a reconciler from its dependencies.
func NewReconciler(deps ReconcilerDeps) *Reconciler {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &Reconciler{
		configPath: deps.ConfigPath,
		registry:   deps.Registry,
		scheduler:  deps.Scheduler,
		manager:    deps.Manager,
		db:         deps.DB,
		snapshot:   deps.Snapshot,
		now:        now,
		baseline:   deps.Baseline,
	}
}

// Reconcile re-reads runwisp.toml and brings the live task set in line with it.
// It returns the diff that was applied, or an error (leaving the live set
// untouched) when the new config fails to load/validate or changes a setting
// that requires a full restart.
func (r *Reconciler) Reconcile() (model.ReloadResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	newCfg, err := config.Load(r.configPath)
	if err != nil {
		return model.ReloadResult{}, fmt.Errorf("reload rejected: %w", err)
	}

	if err := checkNonReloadable(r.baseline, newCfg); err != nil {
		return model.ReloadResult{}, err
	}

	oldTasks := r.registry.Snapshot()
	newTasks := tasksByName(newCfg)
	diff := config.DiffTasks(oldTasks, newTasks)

	r.apply(diff, oldTasks, newTasks)

	r.baseline = newCfg
	r.snapshot.Refresh(r.configPath, newCfg, r.now())

	result := diff.ToResult()
	slog.Info("Configuration reloaded",
		"added", len(result.Added), "removed", len(result.Removed), "changed", len(result.Changed))
	return result, nil
}

// apply mutates the live set in an order that never leaves a half-state:
// removals first, then additions, then in-place changes, then the provenance-only
// restamps.
func (r *Reconciler) apply(diff config.Diff, oldTasks, newTasks map[string]*model.Task) {
	for _, name := range diff.Removed {
		r.applyRemoved(name, oldTasks[name])
	}
	for _, name := range diff.Added {
		r.applyAdded(newTasks[name])
	}
	for _, change := range diff.Changed {
		r.applyChanged(change, oldTasks[change.Name], newTasks[change.Name])
	}
	for _, name := range diff.Restamped {
		r.applyRestamped(newTasks[name])
	}
}

// applyRemoved unschedules, stops, and forgets a task. Cron tasks keep their
// in-flight runs (they drain under the old definition); services are stopped.
func (r *Reconciler) applyRemoved(name string, _ *model.Task) {
	if r.scheduler != nil {
		r.scheduler.RemoveTask(name)
	}
	r.manager.RemoveTask(name)
	r.registry.Delete(name)
}

// applyAdded registers a brand-new task. It is registered for missed-tick
// tracking (idempotent) so a later daemon restart anchors catch-up at "now"
// rather than replaying a backlog. Crucially it does NOT fire run_on_start and
// does NOT run catch-up — those are boot-only.
func (r *Reconciler) applyAdded(task *model.Task) {
	r.registry.Set(task)
	if err := r.db.EnsureTaskRegistered(context.Background(), task.Name, r.now()); err != nil {
		slog.Warn("Failed to register added task for catch-up tracking", "task", task.Name, "err", err)
	}
	r.manager.UpsertTask(task)

	if task.Kind.IsService() {
		// Honours Autostart: a non-autostart service is created stopped, and
		// StartServiceInstances is a no-op while stopped.
		if err := r.manager.StartServiceInstances(task.Name, model.TriggeredByService); err != nil {
			slog.Error("Failed to start instances for added service", "task", task.Name, "err", err)
		}
		return
	}
	if r.scheduler != nil && task.Cron != "" {
		if err := r.scheduler.AddTask(task); err != nil {
			slog.Warn("Failed to schedule added task", "task", task.Name, "err", err)
		}
	}
}

// applyChanged swaps in the new definition. Already-running cron runs keep the
// pointer they captured; new firings use the new one. A schedule/kind change
// reschedules the cron entry; a changed service is recycled so the new command,
// env, or instance count takes effect.
func (r *Reconciler) applyChanged(change config.TaskChange, oldTask, newTask *model.Task) {
	r.registry.Set(newTask)

	// A task that stopped being a service: cancel its old instances before the
	// definition flips so the supervisor doesn't keep refilling them.
	if oldTask.Kind.IsService() && !newTask.Kind.IsService() {
		if err := r.manager.StopService(oldTask.Name); err != nil {
			slog.Warn("Failed to stop instances of de-serviced task", "task", oldTask.Name, "err", err)
		}
	}

	r.manager.UpsertTask(newTask)

	if r.scheduler != nil && (change.Has(config.ReasonSchedule) || change.Has(config.ReasonKind)) {
		r.rescheduleChanged(newTask)
	}

	if newTask.Kind.IsService() {
		r.recycleChangedService(newTask)
	}
}

// applyRestamped swaps in a definition that differs only in derived provenance —
// a task `runwisp promote` graduated out of the staging file into the operator's
// own config. The registry and manager take the new pointer so the API, UI and
// TUI stop reporting it as staged, and that is all: nothing is rescheduled and no
// service is recycled, because what the task runs did not change. Bouncing a
// running service because its definition moved between two files would be a
// surprise the operator never asked for.
func (r *Reconciler) applyRestamped(task *model.Task) {
	r.registry.Set(task)
	r.manager.UpsertTask(task)
}

// rescheduleChanged drops the cron entry for a changed task and re-adds it under
// the new definition when it still has a schedule.
func (r *Reconciler) rescheduleChanged(newTask *model.Task) {
	r.scheduler.RemoveTask(newTask.Name)
	if newTask.Cron != "" {
		if err := r.scheduler.AddTask(newTask); err != nil {
			slog.Warn("Failed to reschedule changed task", "task", newTask.Name, "err", err)
		}
	}
}

// recycleChangedService recycles running instances to pick up the new
// definition, then fills any slots added by an instance-count increase.
func (r *Reconciler) recycleChangedService(newTask *model.Task) {
	if err := r.manager.RestartServiceInstances(newTask.Name); err != nil {
		slog.Error("Failed to restart changed service", "task", newTask.Name, "err", err)
	}
	if err := r.manager.StartServiceInstances(newTask.Name, model.TriggeredByService); err != nil {
		slog.Error("Failed to start instances for changed service", "task", newTask.Name, "err", err)
	}
}

// tasksByName indexes a config's resolved tasks by name. The pointers alias the
// config's Tasks slice, so callers must treat them as read-only.
func tasksByName(cfg *config.Config) map[string]*model.Task {
	out := make(map[string]*model.Task, len(cfg.Tasks))
	for i := range cfg.Tasks {
		t := &cfg.Tasks[i]
		out[t.Name] = t
	}
	return out
}

// checkNonReloadable rejects a reload that touches daemon-wide settings the
// running process can't safely swap. These require a full `runwisp restart`.
// Server bind host/port are CLI flags, not config, so they can't change here.
func checkNonReloadable(old, updated *config.Config) error {
	if old.Daemon != updated.Daemon {
		return nonReloadableErr("[daemon]")
	}
	if old.Scheduler.Timezone != updated.Scheduler.Timezone {
		return nonReloadableErr("[scheduler] timezone")
	}
	if old.Storage != updated.Storage {
		return nonReloadableErr("[storage]")
	}
	if !reflect.DeepEqual(old.Notify, updated.Notify) {
		return nonReloadableErr("[notify]")
	}
	return nil
}

func nonReloadableErr(section string) error {
	return fmt.Errorf("reload rejected: %s changed; requires `runwisp restart`", section)
}
