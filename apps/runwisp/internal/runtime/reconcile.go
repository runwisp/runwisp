// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sync"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/cronprobe"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
)

// Reconciler applies an explicit `runwisp reload` (CLI over the local socket or
// SIGHUP) to the running daemon. It is validate-first and atomic at the config
// layer: the whole runwisp.toml is re-loaded and re-validated, a change to a
// restart-only setting is rejected, and the daemon-wide settings are prepared
// (see SettingsHook), all before a single live task is touched. On rejection
// nothing is applied and the running daemon is unchanged.
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
	settings   SettingsHook
	now        func() time.Time

	mu       sync.Mutex // serialises reloads
	baseline *config.Config

	// cronProbe and stopCronWatcher drive the cron-hold watcher; see
	// WatchCronHolds. Guarded by mu.
	cronProbe       func() cronprobe.State
	stopCronWatcher context.CancelFunc
}

// ReconcilerDeps wires a Reconciler. Baseline is the config the daemon booted
// with (the current live set); the reconciler replaces it after each successful
// reload. Snapshot is re-pinned on success so config_stale reflects the applied
// config. Settings may be nil.
type ReconcilerDeps struct {
	ConfigPath string
	Baseline   *config.Config
	Registry   *TaskRegistry
	Scheduler  *Scheduler
	Manager    TaskManager
	DB         storage.RunRepository
	Snapshot   *config.Snapshot
	Settings   SettingsHook
	Now        func() time.Time
}

// SettingsHook prepares the daemon-wide (non-task) side of a reload: notifiers
// and routes, storage limits, and the [daemon] keys a reload applies live. It
// runs after validation and before anything live changes, and must change
// nothing itself; an error rejects the whole reload. It returns the TOML keys
// the reload changes, reported in the reload result, and the commit that
// applies exactly those. Commit runs before the task set is reconciled, so
// services the reload starts and runs it triggers already see the new
// settings and report through the new notifiers.
type SettingsHook func(old, updated *config.Config) (keys []string, commit func(), err error)

// NewReconciler wires a reconciler from its dependencies.
func NewReconciler(deps ReconcilerDeps) *Reconciler {
	return &Reconciler{
		configPath: deps.ConfigPath,
		registry:   deps.Registry,
		scheduler:  deps.Scheduler,
		manager:    deps.Manager,
		db:         deps.DB,
		snapshot:   deps.Snapshot,
		settings:   deps.Settings,
		now:        deps.Now,
		baseline:   deps.Baseline,
	}
}

// Reconcile re-reads runwisp.toml and brings the live task set in line with it.
// It returns the diff that was applied, or an error (leaving the live set
// untouched) when the new config fails to load/validate, changes a setting
// that requires a full restart, or carries settings the daemon can't apply.
func (r *Reconciler) Reconcile() (model.ReloadResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	newCfg, err := config.Load(r.configPath)
	if err == nil {
		// Boot applied the env override too (loadConfigFile); without it here
		// every reload would see trusted_proxies change while the env pins it.
		err = config.ApplyTrustedProxiesEnv(newCfg)
	}
	if err != nil {
		return model.ReloadResult{}, fmt.Errorf("reload rejected: %w", err)
	}

	// Re-assert the privileged trust check on every reload: the file the root
	// daemon is about to take task definitions from must still be unreachable
	// through a user-writable path or repointable symlink. No-op when unprivileged.
	if err := config.AssertPrivilegedConfigTrust(newCfg, r.configPath); err != nil {
		return model.ReloadResult{}, fmt.Errorf("reload rejected: %w", err)
	}

	if err := CheckNonReloadable(r.baseline, newCfg); err != nil {
		return model.ReloadResult{}, err
	}

	// An unset [daemon] timezone resolves to the host zone on every load. Keep
	// the zone the daemon booted with: the TOML didn't change, and a host zone
	// change is picked up by a restart, not by whichever unrelated reload
	// happens to run next.
	if newCfg.Scheduler.Source == config.TimezoneSourceSystem && r.baseline.Scheduler.Source == config.TimezoneSourceSystem {
		newCfg.Scheduler.Timezone = r.baseline.Scheduler.Timezone
	}

	var settings []string
	var newLoc *time.Location
	if newCfg.Scheduler.Timezone != r.baseline.Scheduler.Timezone {
		if newLoc, err = config.ResolveTimezone("daemon.timezone", newCfg.Scheduler.Timezone); err != nil {
			return model.ReloadResult{}, fmt.Errorf("reload rejected: %w", err)
		}
		settings = append(settings, "daemon.timezone")
	}

	commitSettings := func() {}
	if r.settings != nil {
		keys, commit, err := r.settings(r.baseline, newCfg)
		if err != nil {
			return model.ReloadResult{}, fmt.Errorf("reload rejected: %w", err)
		}
		settings, commitSettings = append(settings, keys...), commit
	}

	oldTasks := r.registry.Snapshot()
	newTasks := tasksByName(newCfg)
	diff := config.DiffTasks(oldTasks, newTasks)

	// Settings first, so what apply starts already runs under them.
	commitSettings()
	if newLoc != nil {
		r.manager.SetDaemonLocation(newLoc)
	}
	applyWarnings := r.apply(diff, oldTasks, newTasks)
	if newLoc != nil && r.scheduler != nil {
		// After apply, so the entries it re-bases are the new task set's.
		applyWarnings = append(applyWarnings, r.scheduler.SetLocation(newLoc, newTasks)...)
	}

	result := diff.ToResult()
	result.Settings = settings
	result.Warnings = append(config.Warnings(newCfg), applyWarnings...)

	r.baseline = newCfg
	r.snapshot.Refresh(r.configPath, newCfg, r.now())
	r.syncCronWatcher()

	slog.Info("Configuration reloaded",
		"added", len(result.Added), "removed", len(result.Removed), "changed", len(result.Changed),
		"settings", result.Settings)
	return result, nil
}

// WatchCronHolds keeps the cron holds honest while the daemon runs, so an
// operator who retires cron gets their jobs back without a `runwisp reload`,
// and a cron that comes back reclaims them before both schedulers fire the same
// job. probe answers whether a system cron daemon is live.
//
// The watcher runs only while the live config reads a crontab: with no
// include_cron the probe could not change a single decision, and running it
// anyway would exec systemctl every minute on every ordinary install. A reload
// that adds include_cron starts it, and one that drops the last crontab stops
// it. The returned func stops watching for good.
func (r *Reconciler) WatchCronHolds(probe func() cronprobe.State) (stop func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cronProbe = probe
	r.syncCronWatcher()
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.cronProbe = nil
		r.syncCronWatcher()
	}
}

// syncCronWatcher (re)starts or stops the cron-hold watcher to match the
// baseline. Called with r.mu held. A running watcher is replaced, not kept: a
// reload re-probes cron, and a watcher still remembering the old answer would
// see a later flip back to it as "no change" and never re-hold the tasks.
func (r *Reconciler) syncCronWatcher() {
	if r.stopCronWatcher != nil {
		// Only cancels; a tick already waiting on r.mu runs once more with a fresh
		// probe answer, which is still the right one to apply.
		r.stopCronWatcher()
		r.stopCronWatcher = nil
	}
	if state, readsCron := config.CronHold(r.baseline); readsCron && r.cronProbe != nil {
		r.stopCronWatcher = StartCronHoldWatcher(r.cronProbe, r.RefreshCronHolds, state)
	}
}

// CronHoldChange is what a cron-liveness refresh moved: the tasks whose hold was
// released (RunWisp now owns their schedule) and the tasks newly held (a cron
// daemon came back and owns them again). Both empty means the machine's answer
// did not change anything.
type CronHoldChange struct {
	Released []string
	Held     []string
}

// RefreshCronHolds applies a fresh cron-liveness answer to the live task set.
//
// It is not a reload: runwisp.toml is not re-read, so config reload stays
// explicit. The only thing re-derived is the machine fact "does a live cron
// daemon own this crontab". Without it, an operator who retires cron and
// forgets to reload has jobs neither scheduler runs.
func (r *Reconciler) RefreshCronHolds(state cronprobe.State) CronHoldChange {
	r.mu.Lock()
	defer r.mu.Unlock()

	updated, changed := config.WithCronHold(r.baseline, state)
	if len(changed) == 0 {
		return CronHoldChange{}
	}
	newTasks := tasksByName(updated)

	var out CronHoldChange
	for _, name := range changed {
		newTask, ok := newTasks[name]
		if !ok {
			continue
		}
		oldTask, ok := r.registry.Get(name)
		if !ok {
			// Removed from the registry since the baseline was pinned. Nothing live
			// to re-own, and inventing a registration here would resurrect it.
			continue
		}
		// The same sequence applyChanged runs for a schedule change, which is what
		// this is: anchorNewlySchedulable stamps the catch-up anchor at the moment
		// RunWisp becomes responsible for the ticks, and rescheduleChanged adds or drops the
		// cron entry according to the new Schedulable().
		r.publishTask(newTask)
		r.anchorNewlySchedulable(oldTask, newTask)
		if r.scheduler != nil {
			r.rescheduleChanged(newTask)
		}
		if newTask.Held() {
			out.Held = append(out.Held, name)
		} else {
			out.Released = append(out.Released, name)
		}
	}

	// A hold flip changes which tasks are schedulable, so the jitter dial must be
	// re-leveled the same way a reload's apply does: a released task otherwise
	// fires at the raw tick until the next reload.
	if r.scheduler != nil && len(out.Held)+len(out.Released) > 0 {
		r.scheduler.RecomputeJitter(newTasks)
	}

	// Last, so Warnings and config.Held answer from the config whose holds are now
	// the ones in force.
	r.baseline = updated
	return out
}

// Warnings reports the live config's non-fatal findings — chiefly the crontab
// jobs include_cron declined to schedule.
//
// It reads the reconciler's baseline rather than a boot-time copy because a
// reload can introduce or fix a skip.
func (r *Reconciler) Warnings() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return config.Warnings(r.baseline)
}

// apply mutates the live set in an order that never leaves a half-state:
// removals first, then additions, then in-place changes, then the provenance-only
// restamps. It returns any non-fatal notices the changes produced (chiefly an
// autostart flip that reload deliberately doesn't act on) for the reload result.
func (r *Reconciler) apply(diff config.Diff, oldTasks, newTasks map[string]*model.Task) []string {
	for _, name := range diff.Removed {
		r.applyRemoved(name)
	}
	if len(diff.Removed) > 0 {
		// A removed task starts fresh if it comes back: forget its first-seen
		// time, last run and pause, or the next boot's catch-up would report the
		// whole time it was gone as missed. Its runs stay.
		if err := r.db.ForgetTaskRegistrationsExcept(context.Background(), slices.Collect(maps.Keys(newTasks))); err != nil {
			slog.Warn("Failed to forget registrations of removed tasks", "err", err)
		}
	}
	for _, name := range diff.Added {
		r.applyAdded(newTasks[name])
	}
	var warnings []string
	for _, change := range diff.Changed {
		if w := r.applyChanged(change, oldTasks[change.Name], newTasks[change.Name]); w != "" {
			warnings = append(warnings, w)
		}
	}
	// Restamped tasks differ only in derived provenance (a task `runwisp promote`
	// graduated out of the staging file), so the new pointer is all they need:
	// nothing is rescheduled and no service is recycled.
	for _, name := range diff.Restamped {
		r.publishTask(newTasks[name])
	}

	// Rebuild jitter plans against the new task set so added and rescheduled
	// tasks get their start-spread without waiting for a restart. Restamps are
	// provenance-only and never touch scheduling, so a pure-restamp reload skips
	// the reshuffle.
	if r.scheduler != nil && (len(diff.Added) > 0 || len(diff.Removed) > 0 || len(diff.Changed) > 0) {
		r.scheduler.RecomputeJitter(newTasks)
	}
	// A pause the new config no longer allows (task removed, turned into a
	// service, cron dropped, manual_trigger = false) is cleared: TOML wins.
	if r.scheduler != nil {
		warnings = append(warnings, r.scheduler.PrunePauses(newTasks)...)
	}
	return warnings
}

// applyRemoved unschedules, stops, and forgets a task. Cron tasks keep their
// in-flight runs (they drain under the old definition); services are stopped.
func (r *Reconciler) applyRemoved(name string) {
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
	// Only a schedulable task is registered, as at boot. A held task stamped now
	// would make the whole hold window look like missed ticks once the hold
	// lifts, and a cron-less one has no ticks to anchor. Either gets its anchor
	// when it becomes schedulable (see anchorNewlySchedulable).
	if task.Schedulable() {
		r.register(task, "added")
	}
	r.publishTask(task)

	if task.Kind.IsService() {
		// Honours Autostart: a non-autostart service is created stopped, and
		// StartServiceInstances is a no-op while stopped.
		if err := r.manager.StartServiceInstances(task.Name, model.TriggeredByService); err != nil {
			slog.Error("Failed to start instances for added service", "task", task.Name, "err", err)
		}
		return
	}
	if r.scheduler != nil && task.Schedulable() {
		if err := r.scheduler.AddTask(task); err != nil {
			slog.Warn("Failed to schedule added task", "task", task.Name, "err", err)
		}
	}
}

// applyChanged swaps in the new definition. Already-running cron runs keep the
// pointer they captured; new firings use the new one. A schedule/kind change
// reschedules the cron entry; a changed service is recycled so the new command,
// env, or instance count takes effect. It returns a non-fatal notice for the
// reload result when the change deliberately does nothing an operator might have
// expected (an autostart flip that reload does not act on).
func (r *Reconciler) applyChanged(change config.TaskChange, oldTask, newTask *model.Task) string {
	// A task that stopped being a service: cancel its old instances before the
	// definition flips so the supervisor doesn't keep refilling them.
	if oldTask.Kind.IsService() && !newTask.Kind.IsService() {
		if err := r.manager.StopService(oldTask.Name); err != nil {
			slog.Warn("Failed to stop instances of de-serviced task", "task", oldTask.Name, "err", err)
		}
	}

	r.publishTask(newTask)
	r.anchorNewlySchedulable(oldTask, newTask)

	if r.scheduler != nil && (change.Has(config.ReasonSchedule) || change.Has(config.ReasonKind)) {
		r.rescheduleChanged(newTask)
	}

	if !newTask.Kind.IsService() {
		return r.autostartFlipWarning(oldTask, newTask)
	}
	if oldTask.Kind.IsService() {
		// A genuine service-definition change: bounce the running instances so
		// the new command, env, or instance count takes effect.
		r.recycleChangedService(newTask)
		return r.autostartFlipWarning(oldTask, newTask)
	}
	// A task→service kind flip: the run in flight under the old non-service
	// definition must finish under it (reload never cancels an in-flight run),
	// so it is left to drain on its own goroutine. Recycling here would cancel
	// it. Only bring the new service up to its instance count, exactly as a
	// freshly added service would; StartServiceInstances honours Autostart / a
	// stopped supervisor.
	r.startChangedService(newTask)
	return ""
}

// autostartFlipWarning surfaces the reload cases where an autostart flip
// silently does nothing an operator plausibly expected, because reload is not a
// restart:
//   - a service that flipped autostart=false→true but stays stopped. The live
//     supervisor is queried rather than inferred from the definitions, so a
//     service the operator started by hand (now running) never triggers it.
//   - a cron task that now has autostart = false but whose schedule is not
//     paused. autostart = false only pauses a task when it is first registered,
//     so a task RunWisp already knew keeps firing.
func (r *Reconciler) autostartFlipWarning(oldTask, newTask *model.Task) string {
	if !newTask.Kind.IsService() {
		if oldTask.StartsPaused() || !newTask.StartsPaused() ||
			(r.scheduler != nil && r.scheduler.IsPaused(newTask.Name)) {
			return ""
		}
		return fmt.Sprintf("task %q has autostart = false but its schedule is not paused; autostart only applies when a task is first added; run 'runwisp pause %s' to pause it now",
			newTask.Name, newTask.Name)
	}
	if oldTask.Autostart || !newTask.Autostart {
		return ""
	}
	snap, ok := r.manager.ServiceSnapshot(newTask.Name)
	if !ok || snap.State != model.ServiceStopped {
		return ""
	}
	return fmt.Sprintf("service %q has autostart=true but is stopped; reload never starts a stopped service; run 'runwisp restart %s' to start it now",
		newTask.Name, newTask.Name)
}

// publishTask makes task's definition the live one in both the registry (what
// the API, UI, and TUI read) and the manager (what new runs use).
func (r *Reconciler) publishTask(task *model.Task) {
	r.registry.Set(task)
	r.manager.UpsertTask(task)
}

// rescheduleChanged drops the cron entry for a changed task and re-adds it under
// the new definition when the schedule is still RunWisp's to fire. A task that
// just became held loses its entry here and gains nothing back, which is how
// retiring-cron-in-reverse (cron coming back) stops RunWisp firing again.
func (r *Reconciler) rescheduleChanged(newTask *model.Task) {
	r.scheduler.RemoveTask(newTask.Name)
	if newTask.Schedulable() {
		if err := r.scheduler.AddTask(newTask); err != nil {
			slog.Warn("Failed to reschedule changed task", "task", newTask.Name, "err", err)
		}
	}
}

// anchorNewlySchedulable stamps the catch-up anchor at the moment a task becomes
// schedulable (its cron hold lifts, or it gains a cron), because that is the
// moment RunWisp becomes responsible for its ticks. Without it the task would
// carry no anchor until the next boot's catch-up pass, and a crash in between
// would leave the real downtime gap unmeasurable: the anchor would be stamped
// at the *restart*, silently swallowing it. It is also the task's first
// registration, so an autostart = false task starts paused here.
//
// INSERT OR IGNORE, so a task that already has an anchor keeps it.
func (r *Reconciler) anchorNewlySchedulable(oldTask, newTask *model.Task) {
	if oldTask.Schedulable() || !newTask.Schedulable() {
		return
	}
	r.register(newTask, "newly scheduled")
}

// register stamps task's catch-up anchor (a no-op once it has one). The first
// registration of an autostart = false task also pauses its schedule: the same
// insert persists the pause, so only the scheduler's in-memory copy is set
// here, before the caller adds the cron entry. what names the occasion in the
// failure log.
func (r *Reconciler) register(task *model.Task, what string) {
	now := r.now()
	paused := task.StartsPaused()
	inserted, err := r.db.EnsureTaskRegistered(context.Background(), task.Name, now, paused)
	if err != nil {
		slog.Warn("Failed to register "+what+" task for catch-up tracking", "task", task.Name, "err", err)
		return
	}
	if inserted && paused && r.scheduler != nil {
		r.scheduler.adoptPause(task.Name, now)
	}
}

// recycleChangedService recycles running instances to pick up the new
// definition, then fills any slots added by an instance-count increase. It
// never revives a service the operator stopped (or one waiting on Autostart)
// and never clears a FATAL instance — reload is not a restart.
func (r *Reconciler) recycleChangedService(newTask *model.Task) {
	if err := r.manager.RecycleServiceInstances(newTask.Name); err != nil {
		slog.Error("Failed to recycle changed service", "task", newTask.Name, "err", err)
	}
}

// startChangedService brings a task that a reload just turned into a service up
// to its instance count without disturbing any run still draining under the
// prior non-service definition. It mirrors applyAdded's service path.
func (r *Reconciler) startChangedService(newTask *model.Task) {
	if err := r.manager.StartServiceInstances(newTask.Name, model.TriggeredByService); err != nil {
		slog.Error("Failed to start instances for newly-serviced task", "task", newTask.Name, "err", err)
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

// CheckNonReloadable rejects a reload that changes a restart-only [daemon] key:
// TLS and the dedicated metrics listener are bound once at boot, and the
// station dispatch gate is fixed into the executor and station client at boot,
// so they require a full `runwisp restart`. Server bind host/port are CLI
// flags, not config, so they can't change here. Exported for the daemon's test
// that every [daemon] key is either restart-only or applied by its SettingsHook.
func CheckNonReloadable(old, updated *config.Config) error {
	o, n := restartOnly(old.Daemon), restartOnly(updated.Daemon)
	switch {
	case o.TLS != n.TLS || o.TLSCert != n.TLSCert || o.TLSKey != n.TLSKey:
		return nonReloadableErr("[daemon] tls")
	case o.MetricsEnabled != n.MetricsEnabled || o.MetricsListen != n.MetricsListen:
		return nonReloadableErr("[daemon] metrics")
	case o.AllowStationDispatch != n.AllowStationDispatch:
		return nonReloadableErr("[daemon] allow_station_dispatch")
	case !reflect.DeepEqual(o, n):
		return nonReloadableErr("[daemon]")
	}
	return nil
}

// restartOnly zeroes the [daemon] keys a reload applies live, leaving the ones
// that need a restart. A key added to config.Daemon later is restart-only
// until it is zeroed here and applied by the daemon's SettingsHook
// (settingsApplier.prepare in cmd/runwisp); TestReloadCoversEveryDaemonKey
// there holds the two in step.
func restartOnly(d config.Daemon) config.Daemon {
	d.ShutdownTimeout = 0
	d.ExternalURL = ""
	d.CheckUpdates = false
	d.TrustedProxies = nil
	return d
}

func nonReloadableErr(section string) error {
	return fmt.Errorf("reload rejected: %s changed; requires `runwisp restart`", section)
}
