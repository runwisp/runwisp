// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"log/slog"
)

func isFailureReason(reason model.EndReason) bool {
	switch reason {
	case model.ReasonFailed, model.ReasonTimeout, model.ReasonCrashed:
		return true
	default:
		return false
	}
}

func shouldRestart(task *model.Task, run *model.Run) bool {
	switch task.Restart {
	case model.RestartAlways:
		return run.EndReason == nil || *run.EndReason != model.ReasonStopped
	case model.RestartOnFailure:
		return run.EndReason != nil && isFailureReason(*run.EndReason)
	default:
		return false
	}
}

func shouldRetry(task *model.Task, run *model.Run) bool {
	if task.Restart != "" && task.Restart != model.RestartNever {
		return false
	}
	if task.RetryAttempts <= 0 || run.EndReason == nil || !isFailureReason(*run.EndReason) {
		return false
	}
	return run.RetryAttempt < task.RetryAttempts
}

// computeRetryDelay calculates the delay before the next retry attempt.
func computeRetryDelay(task *model.Task, attempt int) time.Duration {
	baseDelay := parseRetryDelay(task.RetryDelay)
	if baseDelay <= 0 {
		baseDelay = 5 * time.Second
	}

	const maxDelay = 5 * time.Minute

	switch task.RetryBackoff {
	case "exponential":
		delay := baseDelay * (1 << min(attempt, 30))
		return min(delay, maxDelay)
	case "linear":
		delay := baseDelay * time.Duration(attempt+1)
		return min(delay, maxDelay)
	default:
		return baseDelay
	}
}

func parseRetryDelay(raw string) time.Duration {
	if raw == "" {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0
	}
	return d
}

// scheduleRetry waits for the retry delay and triggers a new run.
func (m *defaultTaskManager) scheduleRetry(task *model.Task, failedRun *model.Run) {
	if m.isShutdown.Load() {
		return
	}

	delay := computeRetryDelay(task, failedRun.RetryAttempt)
	slog.Info("Scheduling retry", "attempt", failedRun.RetryAttempt+1, "max", task.RetryAttempts, "task", task.Name, "delay", delay)

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-m.persistence.Done():
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

func (m *defaultTaskManager) scheduleRestart(task *model.Task, previousRun *model.Run) {
	if m.isShutdown.Load() {
		return
	}

	_, err := m.TriggerRunWithOptions(task.Name, TriggerRunOptions{
		TriggeredBy: previousRun.TriggeredBy,
	})
	if err != nil {
		slog.Error("Restart failed", "task", task.Name, "err", err)
	}
}
