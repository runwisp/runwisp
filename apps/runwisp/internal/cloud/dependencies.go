// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cloud

import (
	"context"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage"
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
	// ListServiceTasks returns copies of every registered service task. The
	// cloud client folds these into the tasks.sync snapshot so cloud-declared
	// services (registered at runtime via service:apply, never in the TOML) are
	// reported as live on this runner.
	ListServiceTasks() []*model.Task
	// UpsertTask installs (or replaces) a task definition. Used by the cloud
	// dispatcher when resolving ad-hoc inline executions.
	UpsertTask(task *model.Task)
	// RemoveTask drops a task from the runner (cancelling service instances and
	// stopping the queue drain). Used by service:remove to tear down a
	// cloud-declared service, which never enters the TOML registry and so has no
	// reconcile-driven removal path.
	RemoveTask(taskName string)
	// TriggerCloudRun starts a fresh cloud-triggered run for the named task,
	// tagged with the supplied external execution id. params carries the
	// dispatch's inputValues — resolved against the task's declared parameters
	// like any manual trigger. Implementations must set
	// TriggeredBy = model.TriggeredByCloud.
	TriggerCloudRun(taskName, executionID string, params map[string]string) (*model.Run, error)
	// TerminateRunByExecutionID cancels a running run identified by
	// the cloud-side execution id, if any.
	TerminateRunByExecutionID(executionID string) error
	// StartServiceInstances brings a service up to its desired instance count.
	StartServiceInstances(taskName string, triggeredBy model.TriggeredBy) error
	// StopService marks a service operator-stopped and cancels its instances.
	StopService(taskName string) error
	// RestartServiceInstances restarts a service's instances.
	RestartServiceInstances(taskName string) error
	// ServiceSnapshot returns the current supervisor view of a service task.
	ServiceSnapshot(taskName string) (model.ServiceSnapshot, bool)
}

// ExternalRunGetter is the subset of run persistence the cloud package needs.
// Mirrors a slice of storage.RunRepository so the cloud doesn't depend on
// the SQLite-backed concrete.
type ExternalRunGetter interface {
	// GetRunByExecutionID returns the run tagged with the supplied
	// cloud-side execution id, or ErrNotFound if no such run exists.
	GetRunByExecutionID(ctx context.Context, executionID string) (*model.Run, error)
}

// PendingLogUploadRepository persists dispatch metadata so the daemon can
// resume terminal log archival after a crash.
type PendingLogUploadRepository interface {
	UpsertPendingLogUpload(ctx context.Context, rec model.PendingLogUpload) error
	DeletePendingLogUpload(ctx context.Context, executionID string) error
	ListPendingLogUploads(ctx context.Context) ([]model.PendingLogUpload, error)
}

// EventSubscriber is the subset of the in-process event hub the cloud bridge
// consumes. Matches *events.Bus's Subscribe signature so the concrete
// implementation satisfies this interface without an adapter.
type EventSubscriber interface {
	Subscribe(eventType events.EventType, handler events.EventHandler) func()
}

// ErrNotFound is the sentinel returned by ExternalRunGetter and related
// interfaces when a referenced execution does not exist. Aliased from the
// storage package so cloud-internal `errors.Is(err, ErrNotFound)` checks
// match what the concrete SQLite-backed repository returns.
var ErrNotFound = storage.ErrNotFound
