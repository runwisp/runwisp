// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/version"
)

// daemonServices holds all long-lived services created during daemon startup.
type daemonServices struct {
	DB                  storage.Database
	EventBus            events.EventBus
	Executor            executor.Executor
	TaskManager         runtime.TaskManager
	TasksMap            map[string]*model.Task
	Scheduler           *runtime.Scheduler
	RetentionCleaner    *runtime.RetentionCleaner
	SoftDeletePurger    *runtime.SoftDeletePurger
	Notify              notifyBundle
	ScheduleResult      runtime.ScheduleResult
	CrashedRuns         int64
	PendingSummary      uikit.PendingRunsSummary
	CatchUpResult       runtime.CatchUpResult
	RunOnStartResult    runtime.RunOnStartResult
	TaskShutdownTimeout time.Duration
	// ServiceLaunchCancel aborts the background depends_on launcher goroutines.
	// Called at the start of graceful shutdown so a dependent still waiting on a
	// dependency doesn't start mid-teardown. No-op in cloud mode (services don't
	// auto-start there).
	ServiceLaunchCancel context.CancelFunc
	// InitWarnings holds non-fatal warnings collected during service init
	// (notify subsystem failures, scheduler start hiccups, etc.) so the
	// caller can render them inside the startup banner instead of having
	// them slog out and fragment the banner above it.
	InitWarnings []string
}

// initDaemonServices creates and wires together the core daemon services.
// db must already be open; initDaemonServices marks crashed runs and then
// builds all higher-level services on top of it.
func initDaemonServices(ctx context.Context, cfg *daemonConfig, db storage.Database, mode daemonMode, f Flags) (*daemonServices, error) {
	// initWarnings accumulates non-fatal startup hiccups so the caller can
	// render them inside the banner instead of emitting slog.Warn lines that
	// would print before the banner exists.
	var initWarnings []string
	addWarning := func(format string, args ...any) {
		initWarnings = append(initWarnings, fmt.Sprintf(format, args...))
	}

	crashed, err := db.MarkCrashedRuns(ctx)
	if err != nil {
		addWarning("Failed to mark crashed runs: %v", err)
	}

	eventBus := events.NewEventBus()

	exec := initExecutor(cfg.Config, eventBus, f.LogDir())

	taskManager, tasksMap := initTaskManager(cfg, db, exec, eventBus)

	var scheduler *runtime.Scheduler
	var schedResult runtime.ScheduleResult
	var pendingSummary uikit.PendingRunsSummary
	var catchUpResult runtime.CatchUpResult
	var runOnStartResult runtime.RunOnStartResult
	// Default to a no-op cancel so cloud mode (no service auto-start) leaves a
	// callable handle in daemonServices.
	serviceLaunchCancel := context.CancelFunc(func() {})

	if mode == modeStandalone {
		// [scheduler] timezone is required at config-load time when any cron task
		// lacks a per-task timezone, so by the point we get here it's either set
		// explicitly or there are no cron expressions to interpret.
		schedLoc, locErr := config.ResolveTimezone("scheduler.timezone", cfg.Config.Scheduler.Timezone)
		if locErr != nil {
			return nil, locErr
		}
		scheduler = runtime.NewScheduler(taskManager, tasksMap, schedLoc)
		schedResult, err = scheduler.Start()
		if err != nil {
			addWarning("Failed to start scheduler: %v", err)
		}

		pendingSummary = resumePendingRuns(ctx, db, taskManager)

		// Fire run_on_start tasks once at boot, before notify so a boot-triggered
		// run doesn't page. Catch-up (which pages on missed runs) is deferred to
		// after notify starts (below) so run.missed events reach a subscriber.
		runOnStartResult = runtime.RunStartupTasks(tasksMap, taskManager)
		if runOnStartResult.Errors > 0 {
			slog.Warn("run_on_start firing completed with errors",
				"triggered", runOnStartResult.Triggered, "errors", runOnStartResult.Errors)
		} else if runOnStartResult.Triggered > 0 {
			slog.Debug("run_on_start firing completed", "triggered", runOnStartResult.Triggered)
		}
		var launchCtx context.Context
		launchCtx, serviceLaunchCancel = context.WithCancel(context.Background())
		startServiceInstances(launchCtx, taskManager, tasksMap)
	}

	retentionCleaner := initRetentionCleaner(cfg, db, tasksMap, f.LogDir())

	softDeletePurger := runtime.NewSoftDeletePurger(db, f.LogDir())
	softDeletePurger.Start()

	notifyB, err := initNotify(cfg, db, eventBus, slog.Default())
	if err != nil {
		addWarning("Failed to initialize notify subsystem: %v", err)
	}
	if notifyB.Service != nil {
		if startErr := notifyB.Service.Start(context.Background()); startErr != nil {
			addWarning("Failed to start notify subsystem: %v", startErr)
		}
	}

	if mode == modeStandalone {
		// Run catch-up now that notify is subscribed, so a missed-run gap
		// detected from persisted history raises a run.missed alert instead of
		// emitting into a bus nobody is listening on yet.
		catchUpResult = runtime.RunMissedTickCatchUp(ctx, db, tasksMap, taskManager, time.Now())
		// Banner already shows the triggered total. Only narrate via slog when
		// something actually went wrong; otherwise stay quiet so INFO output
		// at default verbosity is uncluttered.
		if catchUpResult.Errors > 0 {
			slog.Warn("Missed-tick catch-up completed with errors",
				"triggered", catchUpResult.Triggered, "errors", catchUpResult.Errors)
		} else if catchUpResult.Triggered > 0 {
			slog.Debug("Missed-tick catch-up completed",
				"triggered", catchUpResult.Triggered)
		}
	}

	return &daemonServices{
		DB:                  db,
		EventBus:            eventBus,
		Executor:            exec,
		TaskManager:         taskManager,
		TasksMap:            tasksMap,
		Scheduler:           scheduler,
		RetentionCleaner:    retentionCleaner,
		SoftDeletePurger:    softDeletePurger,
		Notify:              notifyB,
		ScheduleResult:      schedResult,
		CrashedRuns:         crashed,
		PendingSummary:      pendingSummary,
		CatchUpResult:       catchUpResult,
		RunOnStartResult:    runOnStartResult,
		TaskShutdownTimeout: cfg.Config.Daemon.ShutdownTimeout,
		ServiceLaunchCancel: serviceLaunchCancel,
		InitWarnings:        initWarnings,
	}, nil
}

func initExecutor(cfg *config.Config, eventBus events.EventBus, logDir string) executor.Executor {
	dockerBackend := executor.NewLazyContainerBackend()
	composeBackend := executor.NewLazyComposeBackend()

	minFreeDisk := cfg.Storage.MinFreeSpace

	return executor.New(executor.Options{
		LogDir:            logDir,
		EventBus:          eventBus,
		CloudShellEnabled: cfg.IsCloudShellEnabled(),
		HasLocalTasks:     len(cfg.Tasks) > 0,
		Docker:            dockerBackend,
		Compose:           composeBackend,
		MinFreeDisk:       minFreeDisk,
	})
}

func initTaskManager(cfg *daemonConfig, db storage.RunRepository, exec executor.Executor, eventBus events.EventBus) (runtime.TaskManager, map[string]*model.Task) {
	taskManager := runtime.NewTaskManager(exec, eventBus, time.Now)
	taskManager.BindPersistenceHook(func(ctx context.Context, run *model.Run, isNew bool) {
		var dbErr error
		if isNew {
			dbErr = db.CreateRun(ctx, run)
		} else {
			dbErr = db.UpdateRun(ctx, run)
		}
		if dbErr == nil {
			return
		}
		// During graceful shutdown the persist context is cancelled out from
		// under in-flight runs — that is expected, not a real persistence
		// failure. Demote to DEBUG so a clean Ctrl+C doesn't read like a
		// database fault; genuine DB errors still surface at ERROR.
		if errors.Is(dbErr, context.Canceled) {
			slog.Debug("Skipped persisting run during shutdown", "id", run.ID)
			return
		}
		slog.Error("Failed to persist run", "id", run.ID, "err", dbErr)
	})

	tasksMap := make(map[string]*model.Task)
	for i := range cfg.Config.Tasks {
		task := &cfg.Config.Tasks[i]
		tasksMap[task.Name] = task
		taskManager.UpsertTask(task)
	}

	return taskManager, tasksMap
}

func initRetentionCleaner(cfg *daemonConfig, db storage.RunRepository, tasksMap map[string]*model.Task, logDir string) *runtime.RetentionCleaner {
	maxTotalSize := cfg.Config.Storage.MaxSize
	cleaner := runtime.NewRetentionCleaner(db, tasksMap, time.Hour, logDir, maxTotalSize)
	cleaner.Start()
	return cleaner
}

// graceWindow extends a dependency's healthy_after to bound how long a
// dependent waits for it at boot. A dependent that doesn't see its dep go
// healthy within dep.HealthyAfter+graceWindow starts anyway with a WARN — the
// "nothing silently fails" rule means depends_on never deadlocks boot. It is
// deliberately not a TOML key: the window is derived from the dep's own
// healthy_after so operators tune readiness in one place.
const graceWindow = 5 * time.Second

// startServiceInstances brings every service up to its desired instance count
// at boot, honouring depends_on. Each service launches in its own goroutine
// that first waits for its direct dependencies to become healthy (bounded —
// see graceWindow), then starts its instances. Launching in the background
// keeps daemon boot non-blocking; the readiness gates, not the spawn order,
// enforce ordering. ctx is cancelled at shutdown so a dependent still waiting
// on a dependency bails instead of starting mid-teardown.
func startServiceInstances(ctx context.Context, taskManager runtime.TaskManager, tasksMap map[string]*model.Task) {
	for _, task := range orderServicesForStart(tasksMap) {
		go launchServiceWhenReady(ctx, taskManager, tasksMap, task)
	}
}

// launchServiceWhenReady blocks until every direct dependency of task is
// healthy (or its bounded window elapses), then starts the service. If ctx is
// cancelled (daemon shutting down) it returns without starting.
func launchServiceWhenReady(ctx context.Context, taskManager runtime.TaskManager, tasksMap map[string]*model.Task, task *model.Task) {
	for _, dep := range task.DependsOn {
		window := graceWindow
		if d, ok := tasksMap[dep]; ok {
			window += d.HealthyAfter
		}
		depCtx, cancel := context.WithTimeout(ctx, window)
		err := taskManager.WaitServiceHealthy(depCtx, dep)
		cancel()
		if ctx.Err() != nil {
			// Daemon is shutting down — do not start anything.
			return
		}
		if err != nil {
			slog.Warn("dependency not healthy within window; starting dependent anyway",
				"service", task.Name, "dependency", dep, "err", err)
		}
	}
	if err := taskManager.StartServiceInstances(task.Name, model.TriggeredByService); err != nil {
		slog.Error("Failed to start service instances", "task", task.Name, "err", err)
	}
}

// orderServicesForStart returns the service tasks in deterministic spawn order:
// ascending priority, then name as a stable tiebreak. Ranging a Go map is
// randomised, so without this the goroutine spawn order (and thus which
// independent service grabs a shared port first) would differ run to run.
// Non-service tasks are dropped. depends_on ordering is enforced by the
// readiness gates in launchServiceWhenReady, not here — this only breaks ties
// between services that are independently startable.
func orderServicesForStart(tasksMap map[string]*model.Task) []*model.Task {
	services := make([]*model.Task, 0, len(tasksMap))
	for _, task := range tasksMap {
		if task.Kind.IsService() {
			services = append(services, task)
		}
	}
	sort.SliceStable(services, func(i, j int) bool {
		if services[i].Priority != services[j].Priority {
			return services[i].Priority < services[j].Priority
		}
		return services[i].Name < services[j].Name
	})
	return services
}

// orderServicesForStop returns services in reverse-dependency order — every
// dependent appears before the dependencies it relies on — so shutdown can stop
// the top of a dependency chain before the services beneath it. It is the
// reverse of a topological start order (dependencies first), with the
// priority/name spawn order as the tiebreak. depends_on is validated acyclic,
// so the DFS always terminates.
func orderServicesForStop(tasksMap map[string]*model.Task) []*model.Task {
	start := topoStartOrder(tasksMap)
	stop := make([]*model.Task, len(start))
	for i, t := range start {
		stop[len(start)-1-i] = t
	}
	return stop
}

// topoStartOrder returns services so each appears after all services it depends
// on (dependencies first). DFS post-order over the priority/name-sorted roots
// gives a deterministic, dependency-respecting order; the visited set also
// guards the (validated-impossible) cyclic case.
func topoStartOrder(tasksMap map[string]*model.Task) []*model.Task {
	roots := orderServicesForStart(tasksMap)
	byName := make(map[string]*model.Task, len(roots))
	for _, s := range roots {
		byName[s.Name] = s
	}
	visited := make(map[string]bool, len(roots))
	ordered := make([]*model.Task, 0, len(roots))

	var visit func(s *model.Task)
	visit = func(s *model.Task) {
		if visited[s.Name] {
			return
		}
		visited[s.Name] = true
		for _, dep := range s.DependsOn {
			if d, ok := byName[dep]; ok {
				visit(d)
			}
		}
		ordered = append(ordered, s)
	}
	for _, s := range roots {
		visit(s)
	}
	return ordered
}

func resumePendingRuns(ctx context.Context, db storage.RunRepository, taskManager runtime.TaskManager) uikit.PendingRunsSummary {
	pendingRuns, err := db.GetPendingRuns(ctx)
	if err != nil {
		slog.Warn("Failed to query pending runs", "err", err)
		return uikit.PendingRunsSummary{}
	}
	if len(pendingRuns) == 0 {
		return uikit.PendingRunsSummary{}
	}

	pr := taskManager.LoadPendingRuns(pendingRuns)
	return uikit.PendingRunsSummary{
		Total:   len(pendingRuns),
		Resumed: pr.Resumed,
		Queued:  pr.Queued,
		Skipped: pr.Skipped,
		Failed:  pr.Failed,
	}
}

// buildDaemonInfo assembles static identity and capability info for the API.
// configLoadedAt is when the boot path snapshotted runwisp.toml; the dynamic
// config_stale flag is injected per request by the server, not stored here.
func buildDaemonInfo(cfg *daemonConfig, svc *daemonServices, configLoadedAt time.Time, port int) *model.DaemonInfo {
	taskNames := make([]string, 0, len(svc.TasksMap))
	for name := range svc.TasksMap {
		taskNames = append(taskNames, name)
	}
	sort.Strings(taskNames)

	tasks := make([]model.TaskBrief, 0, len(taskNames))
	for _, name := range taskNames {
		j := svc.TasksMap[name]
		tasks = append(tasks, model.TaskBrief{
			Name:          j.Name,
			Kind:          j.Kind,
			Group:         j.Group,
			Cron:          j.Cron,
			APITrigger:    j.APITrigger,
			CatchUp:       j.CatchUp,
			Restart:       j.Restart,
			MaxConcurrent: j.MaxConcurrent,
			OnOverlap:     j.OnOverlap,
			Instances:     j.Instances,
			DependsOn:     j.DependsOn,
			Compose:       j.Compose,
		})
	}

	capInfos := capInfosFromAvailability(svc.Executor.Availability())

	return &model.DaemonInfo{
		Version:          version.Version,
		Fingerprint:      cfg.Fingerprint,
		Port:             port,
		CloudEnabled:     cfg.CloudConfig.Enabled,
		SchedulingActive: svc.Scheduler != nil,
		ServiceManaged:   autostart.RunningUnderServiceManager(),
		AuthDisabled:     cfg.NoAuth,
		ConfigLoadedAt:   configLoadedAt,
		ResolvedTimezone: cfg.Config.Scheduler.Timezone,
		TimezoneSource:   cfg.Config.Scheduler.Source,
		Tasks:            tasks,
		Capabilities:     capInfos,
	}
}

func capInfosFromAvailability(a executor.Availability) []model.CapInfo {
	return []model.CapInfo{
		{Name: "shell", Available: a.Shell.Available},
		{Name: "container", Available: a.Container.Available},
		{Name: "compose", Available: a.Compose.Available},
		{Name: "http", Available: a.HTTP.Available},
		{Name: "config", Available: a.Config.Available},
	}
}
