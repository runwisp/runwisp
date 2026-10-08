// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

// RunTrigger is what the scheduler and the boot helpers (catch-up, run_on_start)
// drive, so they can be tested without a real executor, event bus, or database.
// Method contracts are documented on defaultTaskManager.
type RunTrigger interface {
	TriggerRunWithOptions(taskName string, options TriggerRunOptions) (*model.Run, error)
	ScheduleJitteredRun(taskName string, tick, slot time.Time, window time.Duration)
	RecordSkippedFiring(taskName string, reason model.EndReason, triggeredBy model.TriggeredBy) error
	RecordMissedRun(taskName string, scheduledAt time.Time, reason string) error
}

// TaskRunner is the subset of TaskManager consumed by the server package.
type TaskRunner interface {
	RunTrigger
	UpsertTask(task *model.Task)
	TerminateRun(runID string) error
	GetActiveRunCount(taskName string) int
	GetActiveRuns(taskName string) []*ActiveRun
	StopTask(taskName string) error

	// Service supervision, driven at daemon boot, by REST, and by station.
	StartServiceInstances(taskName string, triggeredBy model.TriggeredBy) error
	StartService(taskName string) error
	StopService(taskName string) error
	RestartServiceInstances(taskName string) error
	ServiceSnapshot(taskName string) (model.ServiceSnapshot, bool)
}

// TaskManager is the full lifecycle interface for task management.
// Daemon bootstrap code and station use this; the server uses the narrower
// TaskRunner.
type TaskManager interface {
	TaskRunner
	GetTask(taskName string) (*model.Task, bool)
	MutateTask(taskName string, mutate func(*model.Task) error) (found bool, err error)
	TerminateRunByExecutionID(executionID string) error
	ListServiceTasks() []*model.Task
	SetSchedulePaused(fn func(taskName string) bool)
	BindPersistenceHook(hook RunPersistenceHook)
	RemoveTask(taskName string)
	LoadPendingRuns(runs []model.Run) PendingRunsResult
	RecycleServiceInstances(taskName string) error
	ServiceHealthy(taskName string) bool
	SetDaemonLocation(loc *time.Location)
	WaitServiceHealthy(ctx context.Context, taskName string) error
	BeginShutdown()
	Shutdown()
	ShutdownWithDeadline(deadline time.Duration)
}
