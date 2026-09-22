// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"context"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage"
)

// The station package depends on these narrow interfaces rather than the
// concrete runtime / storage implementations. Concrete adapters live in
// apps/runwisp/cmd/runwisp/, which keeps the station package decoupled from
// the daemon's persistence and run-management internals.

// TaskRunner is the subset of the runtime task manager that the station
// integration drives. Adapters in cmd/runwisp/ bridge this to the concrete
// runtime.TaskManager.
type TaskRunner interface {
	// GetTask returns a copy of a registered task, if any.
	GetTask(taskName string) (*model.Task, bool)
	// ListServiceTasks returns copies of every registered service task. The
	// station client folds these into the tasks.sync snapshot so station-declared
	// services (registered at runtime via service:apply, never in the TOML) are
	// reported as live on this runner.
	ListServiceTasks() []*model.Task
	// UpsertTask installs (or replaces) a task definition. Used by the station
	// dispatcher when resolving ad-hoc inline executions.
	UpsertTask(task *model.Task)
	// MutateTask atomically reads, mutates, and re-installs a task's live
	// definition under a single lock acquisition, so a service:apply merge
	// cannot be silently clobbered by a concurrent reload touching the same
	// task. found is false (mutate is never called) if the task isn't
	// currently registered by name.
	MutateTask(taskName string, mutate func(*model.Task) error) (found bool, err error)
	// RemoveTask drops a task from the runner (cancelling service instances and
	// stopping the queue drain). Used by service:remove to tear down a
	// station-declared service, which never enters the TOML registry and so has no
	// reconcile-driven removal path.
	RemoveTask(taskName string)
	// TriggerStationRun starts a fresh station-triggered run for the named task,
	// tagged with the supplied external execution id. params carries the
	// dispatch's inputValues — resolved against the task's declared parameters
	// like any manual trigger. Implementations must set
	// TriggeredBy = model.TriggeredByStation.
	TriggerStationRun(taskName, executionID string, params map[string]string) (*model.Run, error)
	// TerminateRunByExecutionID cancels a running run identified by
	// the station-side execution id, if any.
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

// TaskSnapshotter returns the daemon's current TOML-defined task set. The
// station client re-reads it on every sync rather than caching it once, so a
// `runwisp reload` (which mutates the registry live, without a process
// restart) is reflected on the next reconnect. *runtime.TaskRegistry
// satisfies this directly via its existing Snapshot method.
type TaskSnapshotter interface {
	Snapshot() map[string]*model.Task
}

// ExternalRunGetter is the subset of run persistence the station package needs.
// Mirrors a slice of storage.RunRepository so the station doesn't depend on
// the SQLite-backed concrete.
type ExternalRunGetter interface {
	// GetRunByExecutionID returns the run tagged with the supplied
	// station-side execution id, or ErrNotFound if no such run exists.
	GetRunByExecutionID(ctx context.Context, executionID string) (*model.Run, error)
}

// PendingLogUploadRepository persists dispatch metadata so the daemon can
// resume terminal log archival after a crash.
type PendingLogUploadRepository interface {
	UpsertPendingLogUpload(ctx context.Context, rec model.PendingLogUpload) error
	DeletePendingLogUpload(ctx context.Context, executionID string) error
	ListPendingLogUploads(ctx context.Context) ([]model.PendingLogUpload, error)
}

// EventSubscriber is the subset of the in-process event hub the station bridge
// consumes. Matches *events.Bus's Subscribe signature so the concrete
// implementation satisfies this interface without an adapter.
type EventSubscriber interface {
	Subscribe(eventType events.EventType, handler events.EventHandler) func()
}

// ErrNotFound is the sentinel returned by ExternalRunGetter and related
// interfaces when a referenced execution does not exist. Aliased from the
// storage package so station-internal `errors.Is(err, ErrNotFound)` checks
// match what the concrete SQLite-backed repository returns.
var ErrNotFound = storage.ErrNotFound
