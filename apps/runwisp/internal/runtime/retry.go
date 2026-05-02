// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
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
	if err != nil {
		slog.Error("Retry failed", "task", task.Name, "attempt", failedRun.RetryAttempt+1, "err", err)
	}
}

// scheduleRestart waits for the restart delay and respawns the previous
// replica (services) or re-triggers the task (non-services).
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

	options := TriggerRunOptions{
		TriggeredBy: previousRun.TriggeredBy,
	}
	if task.Kind.IsService() {
		idx := previousRun.ReplicaIndex
		options.ReplicaIndex = &idx
	}

	_, err := m.TriggerRunWithOptions(task.Name, options)
	if err != nil {
		slog.Error("Restart failed", "task", task.Name, "replica", previousRun.ReplicaIndex, "err", err)
	}
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
