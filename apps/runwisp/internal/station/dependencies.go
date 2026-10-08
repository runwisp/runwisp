// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"context"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/storage"
)

// TaskRunner is the slice of runtime.TaskManager the station integration
// drives; the daemon passes its task manager directly and tests pass a fake.
// Method contracts are documented on the runtime implementation.
type TaskRunner interface {
	GetTask(taskName string) (*model.Task, bool)
	// ListServiceTasks feeds tasks.sync, so station-declared services (never
	// in the TOML) are reported as live on this runner.
	ListServiceTasks() []*model.Task
	UpsertTask(task *model.Task)
	MutateTask(taskName string, mutate func(*model.Task) error) (found bool, err error)
	// RemoveTask backs service:remove: a station-declared service never enters
	// the TOML registry, so it has no reconcile-driven removal path.
	RemoveTask(taskName string)
	TriggerRunWithOptions(taskName string, options runtime.TriggerRunOptions) (*model.Run, error)
	TerminateRunByExecutionID(executionID string) error
	StartServiceInstances(taskName string, triggeredBy model.TriggeredBy) error
	StartService(taskName string) error
	StopService(taskName string) error
	RestartServiceInstances(taskName string) error
	ServiceSnapshot(taskName string) (model.ServiceSnapshot, bool)
}

// ExternalRunGetter is the subset of run persistence the station package needs.
// Mirrors a slice of storage.RunRepository so the station doesn't depend on
// the SQLite-backed concrete.
type ExternalRunGetter interface {
	// GetRunByExecutionID returns the run tagged with the supplied
	// station-side execution id, or ErrNotFound if no such run exists.
	GetRunByExecutionID(ctx context.Context, executionID string) (*model.Run, error)
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
