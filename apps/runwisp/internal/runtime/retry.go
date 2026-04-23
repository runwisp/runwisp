// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"sync/atomic"
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
	switch task.Execution.Restart {
	case model.RestartAlways:
		return run.EndReason == nil || *run.EndReason != model.ReasonStopped
	case model.RestartOnFailure:
		return run.EndReason != nil && isFailureReason(*run.EndReason)
	default:
		return false
	}
}

func shouldRetry(task *model.Task, run *model.Run) bool {
	if task.Execution.Restart != "" && task.Execution.Restart != model.RestartNever {
		return false
	}
	if task.Retry.Limit <= 0 || run.EndReason == nil || !isFailureReason(*run.EndReason) {
		return false
	}
	return run.RetryAttempt < task.Retry.Limit
}

// computeRetryDelay calculates the delay before the next retry attempt.
func computeRetryDelay(task *model.Task, attempt int) time.Duration {
	baseDelay := time.Duration(task.Retry.DelaySec) * time.Second
	if baseDelay <= 0 {
		baseDelay = 5 * time.Second
	}

	const maxDelay = 5 * time.Minute

	switch task.Retry.Backoff {
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

// scheduleRetry waits for the retry delay and triggers a new run.
func (m *defaultTaskManager) scheduleRetry(task *model.Task, failedRun *model.Run) {
	if atomic.LoadInt32(&m.isShutdown) == 1 {
		return
	}

	delay := computeRetryDelay(task, failedRun.RetryAttempt)
	slog.Info("Scheduling retry", "attempt", failedRun.RetryAttempt+1, "max", task.Retry.Limit, "task", task.Name, "delay", delay)

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-m.persistence.Done():
		return
	}

	if atomic.LoadInt32(&m.isShutdown) == 1 {
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
	if atomic.LoadInt32(&m.isShutdown) == 1 {
		return
	}

	_, err := m.TriggerRunWithOptions(task.Name, TriggerRunOptions{
		TriggeredBy: previousRun.TriggeredBy,
	})
	if err != nil {
		slog.Error("Restart failed", "task", task.Name, "err", err)
	}
}
