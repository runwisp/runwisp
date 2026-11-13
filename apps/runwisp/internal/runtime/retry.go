// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"errors"
	"log/slog"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/runtime/retry"
)

// scheduleRetry waits for the retry delay and triggers a new run.
// stop is closed if the operator stops the task or a reload removes it first.
func (m *defaultTaskManager) scheduleRetry(task *model.Task, failedRun *model.Run, stop <-chan struct{}) {
	if m.isShutdown.Load() {
		return
	}

	delay := retry.ComputeRetryDelay(task, failedRun.RetryAttempt)
	slog.Info("Scheduling retry", "attempt", failedRun.RetryAttempt+1, "max", task.RetryAttempts, "task", task.Name, "delay", delay)

	if !m.waitForDelay(delay, stop) {
		return
	}

	if m.isShutdown.Load() {
		return
	}

	_, err := m.TriggerRunWithOptions(task.Name, TriggerRunOptions{
		TriggeredBy:  failedRun.TriggeredBy,
		RetryAttempt: failedRun.RetryAttempt + 1,
		RetryOfRunID: &failedRun.ID,
		// Carry the originating run's resolved parameters forward — otherwise a
		// retried manual run would silently revert to the declared defaults. The
		// authoritative conversion also keeps params the operator omitted omitted.
		Params:  model.SuppliedFromResolved(task.Parameters, failedRun.Params),
		Overlay: failedRun.Overlay,
	})
	if err != nil && !errors.Is(err, errShuttingDown) {
		slog.Error("Retry failed", "task", task.Name, "attempt", failedRun.RetryAttempt+1, "err", err)
	}
}

// scheduleRestart waits for the restart delay and respawns the previous
// service instance (restart is a service-only policy; see ShouldRestart).
func (m *defaultTaskManager) scheduleRestart(task *model.Task, previousRun *model.Run, attempt int) {
	if m.isShutdown.Load() {
		return
	}

	delay := retry.RestartDelay(task, attempt, previousRun.EndReason)
	if delay > 0 && !m.waitForDelay(delay, nil) {
		return
	}

	if m.isShutdown.Load() {
		return
	}

	if task.Kind.IsService() && m.isServiceStopped(task.Name) {
		return
	}

	options := TriggerRunOptions{
		TriggeredBy: previousRun.TriggeredBy,
		// Preserve the operator's inputs across a restart (no-op for services,
		// which never carry parameters).
		Params:  model.SuppliedFromResolved(task.Parameters, previousRun.Params),
		Overlay: previousRun.Overlay,
	}
	if task.Kind.IsService() {
		options.TriggeredBy = model.TriggeredByService
		idx := previousRun.InstanceIndex
		options.InstanceIndex = &idx
	}

	_, err := m.TriggerRunWithOptions(task.Name, options)
	if err != nil && !errors.Is(err, errShuttingDown) {
		slog.Error("Restart failed", "task", task.Name, "instance", previousRun.InstanceIndex, "err", err)
	}
}

// isServiceStopped reports whether an operator has flagged this service as
// stopped via the REST / TUI controls. The flag is in-memory only and is
// cleared on daemon restart.
func (m *defaultTaskManager) isServiceStopped(taskName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ts, exists := m.tasks[taskName]
	if !exists || ts.supervisor == nil {
		return false
	}
	return ts.supervisor.IsStopped()
}

// waitForDelay sleeps for the given duration, returning false if stop (nil for
// none) is closed or the daemon shuts down before the timer fires. It watches shutdownCtx, which is cancelled
// at the very start of ShutdownWithDeadline — before the wg drain — so a parked
// wait never deadlocks the drain (see defaultTaskManager.shutdownCtx).
func (m *defaultTaskManager) waitForDelay(delay time.Duration, stop <-chan struct{}) bool {
	// Checked first so a stop wins over an already-expired (or zero) delay.
	select {
	case <-stop:
		return false
	default:
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-stop:
		return false
	case <-m.shutdownCtx.Done():
		return false
	}
}
