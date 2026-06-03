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

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/redact"
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
	TaskShutdownTimeout time.Duration
	// Redactor masks hidden resolved secrets in captured output and API DTOs;
	// RevealVars is the set of variable names whose resolved value may be shown.
	// Both are built once at boot from the config's ${...} references.
	Redactor   *redact.Redactor
	RevealVars map[string]bool
	// InitWarnings holds non-fatal warnings collected during service init
	// (crashed-run marking, scheduler start hiccups, etc.) so the caller can
	// render them inside the startup banner instead of having them slog out
	// and fragment the banner above it. Notify misconfiguration is fatal, not
	// a warning — see initNotify's call site.
	InitWarnings []string
}

// initDaemonServices creates and wires together the core daemon services.
// db must already be open; initDaemonServices marks crashed runs and then
// builds all higher-level services on top of it.
func initDaemonServices(ctx context.Context, cfg *daemonConfig, db storage.Database, mode daemonMode) (*daemonServices, error) {
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

	reveal, redactor, err := buildSecretRedaction(cfg.Config)
	if err != nil {
		return nil, err
	}

	exec := initExecutor(cfg.Config, eventBus, redactor)

	taskManager, tasksMap := initTaskManager(cfg, db, exec, eventBus)

	var scheduler *runtime.Scheduler
	var schedResult runtime.ScheduleResult
	var pendingSummary uikit.PendingRunsSummary
	var catchUpResult runtime.CatchUpResult

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

		startServiceInstances(taskManager, tasksMap)
	}

	retentionCleaner := initRetentionCleaner(cfg, db, tasksMap)

	softDeletePurger := runtime.NewSoftDeletePurger(db, flags.LogDir())
	softDeletePurger.Start()

	// A configured notify block that can't be brought up — most commonly a
	// ${VAR} secret whose env var is unset or a ${file:...} that can't be read
	// — is a fatal config error, not a warning. Booting anyway would leave the
	// operator believing alerts are wired when they silently never fire, which
	// violates the prime directive that nothing fails silently. The error names
	// the offending notifier and field, so it tells the operator what to fix.
	notifyB, err := initNotify(cfg, db, eventBus, slog.Default())
	if err != nil {
		return nil, fmt.Errorf("notify subsystem: %w", err)
	}
	if notifyB.Service != nil {
		if startErr := notifyB.Service.Start(context.Background()); startErr != nil {
			return nil, fmt.Errorf("start notify subsystem: %w", startErr)
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
		TaskShutdownTimeout: cfg.Config.Daemon.ShutdownTimeout,
		InitWarnings:        initWarnings,
		Redactor:            redactor,
		RevealVars:          reveal,
	}, nil
}

// buildSecretRedaction turns the config's reveal_vars list into a lookup set and
// seeds a Redactor with every hidden resolved secret. Resolution here re-reads
// the same env/files the load-time presence check already verified, so an error
// is fatal — it means a referenced ${VAR}/file went missing between validate and
// boot. Forgetting to list a name in reveal_vars hides its value (safe).
func buildSecretRedaction(cfg *config.Config) (map[string]bool, *redact.Redactor, error) {
	reveal := make(map[string]bool, len(cfg.Daemon.RevealVars))
	for _, name := range cfg.Daemon.RevealVars {
		reveal[name] = true
	}
	hidden, err := config.CollectHiddenSecrets(cfg, flags.DataDir, reveal)
	if err != nil {
		return nil, nil, err
	}
	redactor := redact.New()
	for _, v := range hidden {
		redactor.Add(v)
	}
	return reveal, redactor, nil
}

func initExecutor(cfg *config.Config, eventBus events.EventBus, redactor *redact.Redactor) executor.Executor {
	// One resolver per daemon: it turns ${...} in shell scripts and inline env
	// values into their real values at spawn, and re-seeds the redactor with
	// file-sourced secrets read at run time (file contents can change post-boot).
	resolver := &executor.SecretResolver{DataDir: flags.DataDir, Redactor: redactor}

	dockerBackend := executor.NewLazyContainerBackend()
	composeBackend := executor.NewLazyComposeBackend(resolver)

	minFreeDisk := cfg.Storage.MinFreeSpace

	return executor.New(executor.Options{
		LogDir:            flags.LogDir(),
		EventBus:          eventBus,
		CloudShellEnabled: cfg.IsCloudShellEnabled(),
		HasLocalTasks:     len(cfg.Tasks) > 0,
		Docker:            dockerBackend,
		Compose:           composeBackend,
		MinFreeDisk:       minFreeDisk,
		Resolver:          resolver,
		Redactor:          redactor,
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

func initRetentionCleaner(cfg *daemonConfig, db storage.RunRepository, tasksMap map[string]*model.Task) *runtime.RetentionCleaner {
	maxTotalSize := cfg.Config.Storage.MaxSize
	cleaner := runtime.NewRetentionCleaner(db, tasksMap, time.Hour, flags.LogDir(), maxTotalSize)
	cleaner.Start()
	return cleaner
}

func startServiceInstances(taskManager runtime.TaskManager, tasksMap map[string]*model.Task) {
	for _, task := range tasksMap {
		if !task.Kind.IsService() {
			continue
		}
		if err := taskManager.StartServiceInstances(task.Name, model.TriggeredByService); err != nil {
			slog.Error("Failed to start service instances", "task", task.Name, "err", err)
		}
	}
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
func buildDaemonInfo(cfg *daemonConfig, svc *daemonServices) *model.DaemonInfo {
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
			Compose:       j.Compose,
		})
	}

	capInfos := capInfosFromAvailability(svc.Executor.Availability())

	return &model.DaemonInfo{
		Version:          version.Version,
		Fingerprint:      cfg.Fingerprint,
		Port:             flags.Port,
		CloudEnabled:     cfg.CloudConfig.Enabled,
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
