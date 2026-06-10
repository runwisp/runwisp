// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"errors"
	"log/slog"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime/retry"
)

// scheduleRetry waits for the retry delay and triggers a new run.
func (m *defaultTaskManager) scheduleRetry(task *model.Task, failedRun *model.Run) {
	if m.isShutdown.Load() {
		return
	}

	delay := retry.ComputeRetryDelay(task, failedRun.RetryAttempt)
	slog.Info("Scheduling retry", "attempt", failedRun.RetryAttempt+1, "max", task.RetryAttempts, "task", task.Name, "delay", delay)

	if !m.waitForDelay(delay) {
		return
	}

	if m.isShutdown.Load() {
		return
	}

	_, err := m.TriggerRunWithOptions(task.Name, TriggerRunOptions{
		TriggeredBy:  failedRun.TriggeredBy,
		RetryAttempt: failedRun.RetryAttempt + 1,
		RetryOfRunID: &failedRun.ID,
	})
	if err != nil && !errors.Is(err, errShuttingDown) {
		slog.Error("Retry failed", "task", task.Name, "attempt", failedRun.RetryAttempt+1, "err", err)
	}
}

// scheduleRestart waits for the restart delay and respawns the previous
// instance (services) or re-triggers the task (non-services).
func (m *defaultTaskManager) scheduleRestart(task *model.Task, previousRun *model.Run, attempt int) {
	if m.isShutdown.Load() {
		return
	}

	delay := retry.ComputeRestartDelay(task, attempt)
	if delay > 0 && !m.waitForDelay(delay) {
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

// waitForDelay sleeps for the given duration, returning false if the daemon
// shuts down before the timer fires.
func (m *defaultTaskManager) waitForDelay(delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-m.persistence.Done():
		return false
	}
}
