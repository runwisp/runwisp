// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
)

// The cloud package depends on these narrow interfaces rather than the
// concrete runtime / storage implementations. Concrete adapters live in
// apps/runwisp/cmd/runwisp/, which keeps the cloud package decoupled from
// the daemon's persistence and run-management internals.

// TaskRunner is the subset of the runtime task manager that the cloud
// integration drives. Adapters in cmd/runwisp/ bridge this to the concrete
// runtime.TaskManager.
type TaskRunner interface {
	// GetTask returns a copy of a registered task, if any.
	GetTask(taskName string) (*model.Task, bool)
	// UpsertTask installs (or replaces) a task definition. Used by the cloud
	// dispatcher when resolving ad-hoc inline executions.
	UpsertTask(task *model.Task)
	// TriggerCloudRun starts a fresh cloud-triggered run for the named task,
	// tagged with the supplied external execution id. Implementations must set
	// TriggeredBy = model.TriggeredByCloud.
	TriggerCloudRun(taskName, externalExecutionID string) (*model.Run, error)
	// TerminateRunByExternalExecutionID cancels a running run identified by
	// the cloud-side execution id, if any.
	TerminateRunByExternalExecutionID(externalExecutionID string) error
}

// RunRepository is the subset of run persistence the cloud package needs.
// Mirrors a slice of storage.RunRepository so the cloud doesn't depend on
// the SQLite-backed concrete.
type RunRepository interface {
	// GetRunByExternalExecutionID returns the run tagged with the supplied
	// cloud-side execution id, or ErrNotFound if no such run exists.
	GetRunByExternalExecutionID(externalExecutionID string) (*model.Run, error)
}

// PendingLogUploadRepository persists dispatch metadata so the daemon can
// resume terminal log archival after a crash.
type PendingLogUploadRepository interface {
	UpsertPendingLogUpload(rec model.PendingLogUpload) error
	DeletePendingLogUpload(externalExecutionID string) error
	ListPendingLogUploads() ([]model.PendingLogUpload, error)
}

// EventBus is the subset of the in-process event hub the cloud bridge
// consumes. Matches events.EventBus's Subscribe signature so the concrete
// implementation satisfies this interface without an adapter.
type EventBus interface {
	Subscribe(eventType events.EventType, handler events.EventHandler) func()
}

// ErrNotFound is model.ErrNotFound re-exported so callers inside the cloud
// package can reference it without importing internal/model directly.
var ErrNotFound = model.ErrNotFound
