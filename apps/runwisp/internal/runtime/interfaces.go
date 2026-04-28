// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import "github.com/runwisp/runwisp/internal/model"

// TaskRunner is the subset of TaskManager consumed by the cloud and server packages.
// Using this interface instead of the concrete implementation makes those packages
// testable without a real executor, event bus, or database.
type TaskRunner interface {
	TriggerRun(taskName string, triggeredBy model.TriggeredBy) (*model.Run, error)
	TriggerRunWithOptions(taskName string, options TriggerRunOptions) (*model.Run, error)
	GetTask(taskName string) (*model.Task, bool)
	UpsertTask(task *model.Task)
	TerminateRun(runID string) error
	TerminateRunByExternalExecutionID(externalExecutionID string) error
	RestartServiceReplicas(taskName string) error
}

// TaskManager is the full lifecycle interface for task management.
// Daemon bootstrap code uses this; consumer packages use the narrower TaskRunner.
type TaskManager interface {
	TaskRunner
	BindPersistenceHook(hook RunPersistenceHook)
	GetActiveRuns(taskName string) []*ActiveRun
	LoadPendingRuns(runs []model.Run) PendingRunsResult
	StartServiceReplicas(taskName string) error
	Shutdown()
}

// ScheduleSource is the subset of Scheduler consumed by the server package.
type ScheduleSource interface {
	GetNextRun(taskName string) *string
}
