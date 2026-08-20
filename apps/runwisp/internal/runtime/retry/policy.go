// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package retry holds pure policy functions for the runtime's retry and
// service-restart loops. Everything here is stateless and side-effect free:
// the runtime decides whether to retry/restart by calling these helpers and
// owns the timer/goroutine plumbing itself.
package retry

import (
	"time"

	"github.com/runwisp/runwisp/internal/model"
)

// RestartBackoffCap caps the exponential restart delay for service instances.
const RestartBackoffCap = 60 * time.Second

// retryDelayCap caps the retry delay regardless of backoff curve.
const retryDelayCap = 5 * time.Minute

// IsFailureReason reports whether the given EndReason represents a failure
// (and therefore makes the run a candidate for retry). ReasonSkipped is
// excluded by design — a skip is the concurrency policy working as intended,
// not a failure to retry.
func IsFailureReason(reason model.EndReason) bool {
	switch reason {
	case model.ReasonFailed, model.ReasonTimeout, model.ReasonCrashed, model.ReasonLogOverflow, model.ReasonStartFailed:
		return true
	default:
		return false
	}
}

// ShouldRestart reports whether a finished run should trigger a restart per
// the task's restart policy.
func ShouldRestart(task *model.Task, run *model.Run) bool {
	// Services are supervisor-managed, so an operator cancel (the Restart button
	// or a single-instance stop, both exiting with ReasonStopped) must refill the
	// slot regardless of restart policy — otherwise restarting an on_failure
	// service just stops it. Whether the service should actually stay down is
	// decided downstream by the supervisor's operator-stop flag (Stop Service and
	// task removal set it before cancelling), not here.
	if task.Kind.IsService() && run.EndReason != nil && *run.EndReason == model.ReasonStopped {
		return true
	}
	switch task.Restart {
	case model.RestartAlways:
		// Services are supervisor-managed: every instance exit refills the slot,
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
	base := task.RetryDelay
	if base <= 0 {
		base = 5 * time.Second
	}
	return computeBackoff(task.RetryBackoff, base, attempt, retryDelayCap)
}

// RestartDelay is the delay before respawning a service instance that exited
// for the given reason. Operator-initiated exits (the Restart button and
// single-instance stop, both surfacing as ReasonStopped) refill immediately —
// the restart backoff throttles crash loops, not deliberate operator actions.
func RestartDelay(task *model.Task, attempt int, reason *model.EndReason) time.Duration {
	if reason != nil && *reason == model.ReasonStopped {
		return 0
	}
	return ComputeRestartDelay(task, attempt)
}

// ComputeRestartDelay calculates the delay before a service instance is
// re-spawned after exiting. attempt is the number of consecutive prior
// restarts without a healthy run (a run that lived past the supervisor's
// configured healthy_after).
func ComputeRestartDelay(task *model.Task, attempt int) time.Duration {
	base := task.RestartDelay
	if base <= 0 {
		base = time.Second
	}
	if attempt <= 0 {
		return base
	}
	return computeBackoff(task.RestartBackoff, base, attempt, RestartBackoffCap)
}

func computeBackoff(strategy model.BackoffCurve, base time.Duration, attempt int, cap time.Duration) time.Duration {
	var delay time.Duration
	switch strategy {
	case model.BackoffExponential:
		delay = base * (1 << min(attempt, 30))
	case model.BackoffLinear:
		delay = base * time.Duration(attempt+1)
	default:
		return base
	}
	if delay > cap || delay <= 0 {
		return cap
	}
	return delay
}
