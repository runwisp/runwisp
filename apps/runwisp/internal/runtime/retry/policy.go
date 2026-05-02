// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package retry holds pure policy functions for the runtime's retry and
// service-restart loops. Everything here is stateless and side-effect free:
// the runtime decides whether to retry/restart by calling these helpers and
// owns the timer/goroutine plumbing itself.
package retry

import (
	"time"

	"github.com/runwisp/runwisp/internal/model"
)

// RestartBackoffCap caps the exponential restart delay for service replicas.
const RestartBackoffCap = 60 * time.Second

// BackoffResetThreshold is the minimum run duration that resets a service's
// per-replica restart attempt counter. Runs that last at least this long are
// treated as "the service was healthy" — the next failure starts the backoff
// over from the base delay.
const BackoffResetThreshold = 60 * time.Second

// retryDelayCap caps the retry delay regardless of backoff curve.
const retryDelayCap = 5 * time.Minute

// IsFailureReason reports whether the given EndReason represents a failure
// (and therefore makes the run a candidate for retry).
func IsFailureReason(reason model.EndReason) bool {
	switch reason {
	case model.ReasonFailed, model.ReasonTimeout, model.ReasonCrashed:
		return true
	default:
		return false
	}
}

// ShouldRestart reports whether a finished run should trigger a restart per
// the task's restart policy.
func ShouldRestart(task *model.Task, run *model.Run) bool {
	switch task.Restart {
	case model.RestartAlways:
		// Services are supervisor-managed: every replica exit refills the slot,
		// including manual stops and service-restart cancellations. The
		// daemon-wide shutdown guard prevents restart loops during teardown.
		if task.Kind.IsService() {
			return true
		}
		return run.EndReason == nil || *run.EndReason != model.ReasonStopped
	case model.RestartOnFailure:
		return run.EndReason != nil && IsFailureReason(*run.EndReason)
	default:
		return false
	}
}

// ShouldRetry reports whether a finished run is eligible for a retry. Restart
// policy takes precedence: if a task restarts, it never retries.
func ShouldRetry(task *model.Task, run *model.Run) bool {
	if task.Restart != "" && task.Restart != model.RestartNever {
		return false
	}
	if task.RetryAttempts <= 0 || run.EndReason == nil || !IsFailureReason(*run.EndReason) {
		return false
	}
	return run.RetryAttempt < task.RetryAttempts
}

// ComputeRetryDelay calculates the delay before the next retry attempt.
func ComputeRetryDelay(task *model.Task, attempt int) time.Duration {
	baseDelay := task.RetryDelay
	if baseDelay <= 0 {
		baseDelay = 5 * time.Second
	}

	switch task.RetryBackoff {
	case "exponential":
		delay := baseDelay * (1 << min(attempt, 30))
		return min(delay, retryDelayCap)
	case "linear":
		delay := baseDelay * time.Duration(attempt+1)
		return min(delay, retryDelayCap)
	default:
		return baseDelay
	}
}

// ComputeRestartDelay calculates the delay before a service replica is
// re-spawned after exiting. attempt is the number of consecutive prior
// restarts without a healthy (>= BackoffResetThreshold) run.
func ComputeRestartDelay(task *model.Task, attempt int) time.Duration {
	base := task.RestartDelay
	if base <= 0 {
		base = time.Second
	}
	if attempt <= 0 || task.RestartBackoff != model.RestartBackoffExponential {
		return base
	}
	delay := base * (1 << min(attempt, 30))
	if delay > RestartBackoffCap || delay <= 0 {
		return RestartBackoffCap
	}
	return delay
}
