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
		// Services are supervisor-managed: every replica exit refills the slot,
		// including manual stops and service-restart cancellations. The
		// daemon-wide shutdown guard in scheduleRestart prevents restart loops
		// during teardown.
		if task.Kind.IsService() {
			return true
		}
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

// restartBackoffResetThreshold is the minimum run duration that resets a
// service's per-replica restart attempt counter. Runs that last at least this
// long are treated as "the service was healthy" — the next failure starts the
// backoff over from the base delay. Not configurable in v1.
const restartBackoffResetThreshold = 60 * time.Second

// restartBackoffCap caps the exponential restart delay.
const restartBackoffCap = 60 * time.Second

// computeRetryDelay calculates the delay before the next retry attempt.
func computeRetryDelay(task *model.Task, attempt int) time.Duration {
	baseDelay := parseDuration(task.RetryDelay, 0)
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

// computeRestartDelay calculates the delay before a service replica is
// re-spawned after exiting. attempt is the number of consecutive prior
// restarts without a healthy (>= restartBackoffResetThreshold) run.
func computeRestartDelay(task *model.Task, attempt int) time.Duration {
	base := parseDuration(task.RestartDelay, time.Second)
	if attempt <= 0 || task.RestartBackoff != model.RestartBackoffExponential {
		return base
	}
	delay := base * (1 << min(attempt, 30))
	if delay > restartBackoffCap || delay <= 0 {
		return restartBackoffCap
	}
	return delay
}

func parseDuration(raw string, fallback time.Duration) time.Duration {
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
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

func (m *defaultTaskManager) scheduleRestart(task *model.Task, previousRun *model.Run, attempt int) {
	if m.isShutdown.Load() {
		return
	}

	delay := computeRestartDelay(task, attempt)
	if delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
		case <-m.persistence.Done():
			timer.Stop()
			return
		}
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
