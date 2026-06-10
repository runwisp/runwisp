// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"context"
	"time"

	"github.com/runwisp/runwisp/internal/model"
)

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
	RestartServiceInstances(taskName string) error
	// StopService marks a service as operator-stopped (in-memory only, cleared
	// on daemon restart) and cancels every live instance. The supervisor will
	// not refill slots until StartServiceInstances is called.
	StopService(taskName string) error
	// RecordSkippedFiring persists a run that was suppressed before the
	// executor started — currently used by the scheduler to log DST wall-clock
	// duplicates with end_reason = "dst_skipped".
	RecordSkippedFiring(taskName string, reason model.EndReason, triggeredBy model.TriggeredBy) error
	// RecordMissedRun persists one terminal end_reason = "missed" run as the
	// browsable record of a downtime gap, then publishes a failure-level event
	// carrying reason as the human sentence. scheduledAt is the latest missed
	// tick and becomes the row's CreatedAt — the anchor that prevents
	// re-alerting on the next restart. Used by missed-tick catch-up.
	RecordMissedRun(taskName string, scheduledAt time.Time, reason string) error
	// GetActiveRunCount reports how many runs for the given task are currently
	// in flight. Unknown tasks return 0.
	GetActiveRunCount(taskName string) int
}

// TaskManager is the full lifecycle interface for task management.
// Daemon bootstrap code uses this; consumer packages use the narrower TaskRunner.
type TaskManager interface {
	TaskRunner
	BindPersistenceHook(hook RunPersistenceHook)
	GetActiveRuns(taskName string) []*ActiveRun
	LoadPendingRuns(runs []model.Run) PendingRunsResult
	StartServiceInstances(taskName string, triggeredBy model.TriggeredBy) error
	// ServiceHealthy reports whether a service has at least one instance that
	// has been running for at least its healthy_after — the live readiness
	// signal depends_on boot gating waits on. Non-services report false.
	ServiceHealthy(taskName string) bool
	// WaitServiceHealthy blocks until ServiceHealthy is true, the context is
	// cancelled, or the service can no longer reach healthy without operator
	// intervention. It returns nil only when the service became healthy.
	WaitServiceHealthy(ctx context.Context, taskName string) error
	// Shutdown cancels every active run and waits for all goroutines to
	// drain. Equivalent to ShutdownWithDeadline(0).
	Shutdown()
	// ShutdownWithDeadline cancels every active run and waits up to the
	// supplied deadline for goroutines to exit. Survivors are SIGKILLed and
	// recorded with end_reason = "daemon_stopped".
	ShutdownWithDeadline(deadline time.Duration)
}

// NextRunGetter is the subset of Scheduler consumed by the server package.
type NextRunGetter interface {
	GetNextRun(taskName string) *string
}
