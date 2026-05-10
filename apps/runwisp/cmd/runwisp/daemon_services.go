// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"sort"
	"time"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/runwisp/runwisp/internal/tui"
	"github.com/runwisp/runwisp/internal/version"
	"log/slog"
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
	Notify              notifyBundle
	ScheduleResult      runtime.ScheduleResult
	CrashedRuns         int64
	PendingSummary      tui.PendingRunsSummary
	CatchUpResult       runtime.CatchUpResult
	TaskShutdownTimeout time.Duration
}

// initDaemonServices creates and wires together the core daemon services.
// db must already be open; initDaemonServices marks crashed runs and then
// builds all higher-level services on top of it.
func initDaemonServices(cfg *daemonConfig, db storage.Database, mode daemonMode) (*daemonServices, error) {
	crashed, err := db.MarkCrashedRuns()
	if err != nil {
		slog.Warn("Failed to mark crashed runs", "err", err)
	}

	eventBus := events.NewEventBus()

	exec := initExecutor(cfg.Config, eventBus)

	taskManager, tasksMap := initTaskManager(cfg, db, exec, eventBus)

	var scheduler *runtime.Scheduler
	var schedResult runtime.ScheduleResult
	var pendingSummary tui.PendingRunsSummary
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
			slog.Warn("Failed to start scheduler", "err", err)
		}

		pendingSummary = resumePendingRuns(db, taskManager)

		catchUpResult = runtime.RunMissedTickCatchUp(db, tasksMap, taskManager, time.Now())
		if catchUpResult.Triggered > 0 {
			slog.Info("Missed-tick catch-up completed", "triggered", catchUpResult.Triggered, "errors", catchUpResult.Errors)
		}

		startServiceReplicas(taskManager, tasksMap)
	}

	retentionCleaner := initRetentionCleaner(cfg, db, tasksMap)

	notifyB, err := initNotify(cfg, db, eventBus, slog.Default())
	if err != nil {
		slog.Warn("Failed to initialize notify subsystem", "err", err)
	}
	if notifyB.Service != nil {
		if startErr := notifyB.Service.Start(context.Background()); startErr != nil {
			slog.Warn("Failed to start notify subsystem", "err", startErr)
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
		Notify:              notifyB,
		ScheduleResult:      schedResult,
		CrashedRuns:         crashed,
		PendingSummary:      pendingSummary,
		CatchUpResult:       catchUpResult,
		TaskShutdownTimeout: cfg.Config.Daemon.ShutdownTimeout,
	}, nil
}

func initExecutor(cfg *config.Config, eventBus events.EventBus) executor.Executor {
	dockerBackend := executor.NewLazyContainerBackend()

	minFreeDisk := cfg.Storage.MinFreeSpace

	return executor.New(executor.Options{
		LogDir:            flags.LogDir(),
		EventBus:          eventBus,
		CloudShellEnabled: cfg.IsCloudShellEnabled(),
		HasLocalTasks:     len(cfg.Tasks) > 0,
		Docker:            dockerBackend,
		MinFreeDisk:       minFreeDisk,
	})
}

func initTaskManager(cfg *daemonConfig, db storage.RunRepository, exec executor.Executor, eventBus events.EventBus) (runtime.TaskManager, map[string]*model.Task) {
	taskManager := runtime.NewTaskManager(exec, eventBus)
	taskManager.BindPersistenceHook(func(run *model.Run, isNew bool) {
		var dbErr error
		if isNew {
			dbErr = db.CreateRun(run)
		} else {
			dbErr = db.UpdateRun(run)
		}
		if dbErr != nil {
			slog.Error("Failed to persist run", "id", run.ID, "err", dbErr)
		}
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

func startServiceReplicas(taskManager runtime.TaskManager, tasksMap map[string]*model.Task) {
	for _, task := range tasksMap {
		if !task.Kind.IsService() {
			continue
		}
		if err := taskManager.StartServiceReplicas(task.Name); err != nil {
			slog.Error("Failed to start service replicas", "task", task.Name, "err", err)
		}
	}
}

func resumePendingRuns(db storage.RunRepository, taskManager runtime.TaskManager) tui.PendingRunsSummary {
	pendingRuns, err := db.GetPendingRuns()
	if err != nil {
		slog.Warn("Failed to query pending runs", "err", err)
		return tui.PendingRunsSummary{}
	}
	if len(pendingRuns) == 0 {
		return tui.PendingRunsSummary{}
	}

	pr := taskManager.LoadPendingRuns(pendingRuns)
	return tui.PendingRunsSummary{
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
		})
	}

	capInfos := capInfosFromAvailability(svc.Executor.Availability())

	return &model.DaemonInfo{
		Version:      version.Version,
		Fingerprint:  cfg.Fingerprint,
		Port:         flags.Port,
		CloudEnabled: cfg.CloudConfig.Enabled,
		Tasks:        tasks,
		Capabilities: capInfos,
	}
}

func capInfosFromAvailability(a executor.Availability) []model.CapInfo {
	return []model.CapInfo{
		{Name: "shell", Available: a.Shell.Available},
		{Name: "container", Available: a.Container.Available},
		{Name: "http", Available: a.HTTP.Available},
		{Name: "config", Available: a.Config.Available},
	}
}
