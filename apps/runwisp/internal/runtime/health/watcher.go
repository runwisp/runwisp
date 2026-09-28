// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package health decides, from a service's [services.*.health_check] probe,
// when one live instance is healthy and when it has to be stopped as
// unhealthy. It is pure policy: running the probe, reading the clock,
// sleeping, and acting on the verdict are all injected, so the whole decision
// sequence is deterministic under test.
package health

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime/retry"
)

// Watcher watches one service instance through its health-check probe.
//
// Before the probe first passes, failures are only reported: the instance is
// still starting. It has until Started+HealthyAfter for that first pass — no
// probe starts after the deadline — and missing it makes the instance
// unhealthy. After the first pass, a failed check is retried the way a failed
// task run is (Probe's retry_attempts / retry_delay / retry_backoff), and a
// check still failing once the retries run out makes the instance unhealthy.
// Checks follow Probe's cron; ticks that fall while a check or a retry chain
// is running are skipped, never queued.
type Watcher struct {
	Probe        *model.Task
	Schedule     cron.Schedule
	Started      time.Time
	HealthyAfter time.Duration

	Now func() time.Time
	// Sleep blocks for d and reports whether ctx is still live afterwards.
	Sleep func(ctx context.Context, d time.Duration) bool
	// Check runs the probe once.
	Check func(ctx context.Context) executor.ProbeResult

	// OnHealthy is called once, when the probe first passes.
	OnHealthy func()
	// Annotate reports what the watcher observed, for the instance's log.
	Annotate func(msg string)
}

// Watch runs until ctx ends, returning false, or until the instance has to be
// stopped as unhealthy, returning true. The reason has been annotated by then.
func (w *Watcher) Watch(ctx context.Context) (unhealthy bool) {
	if !w.awaitFirstPass(ctx) {
		return ctx.Err() == nil
	}
	w.Annotate("health check passed; the instance is healthy")
	w.OnHealthy()
	for {
		now := w.Now()
		if !w.Sleep(ctx, w.Schedule.Next(now).Sub(now)) {
			return false
		}
		res, done := w.check(ctx)
		if done {
			return false
		}
		if !w.passed(res) && !w.recovers(ctx, res) {
			return ctx.Err() == nil
		}
	}
}

// awaitFirstPass checks until the probe first passes, reporting whether it
// did. A false return with ctx still live means the healthy_after deadline
// went by first (already annotated). Failures until then are only reported.
func (w *Watcher) awaitFirstPass(ctx context.Context) bool {
	deadline := w.Started.Add(w.HealthyAfter)
	for {
		now := w.Now()
		next := w.Schedule.Next(now)
		if !next.Before(deadline) {
			if w.Sleep(ctx, deadline.Sub(now)) {
				w.Annotate(fmt.Sprintf("health check did not pass within healthy_after (%s); stopping the instance as unhealthy", w.HealthyAfter))
			}
			return false
		}
		if !w.Sleep(ctx, next.Sub(now)) {
			return false
		}
		res, done := w.check(ctx)
		if done {
			return false
		}
		if w.passed(res) {
			return true
		}
		w.Annotate("health check failed, ignored until it first passes: " + w.describe(res))
	}
}

// recovers runs the retry chain after a failed check on a healthy instance,
// reporting whether a retry passed. A false return with ctx still live means
// every retry failed and the instance is unhealthy (already annotated).
func (w *Watcher) recovers(ctx context.Context, res executor.ProbeResult) bool {
	checks := w.Probe.RetryAttempts + 1
	w.Annotate(fmt.Sprintf("health check failed (1/%d): %s", checks, w.describe(res)))
	for attempt := 0; attempt < w.Probe.RetryAttempts; attempt++ {
		if !w.Sleep(ctx, retry.ComputeRetryDelay(w.Probe, attempt)) {
			return false
		}
		res, done := w.check(ctx)
		if done {
			return false
		}
		if w.passed(res) {
			w.Annotate("health check passed again; the instance is healthy")
			return true
		}
		w.Annotate(fmt.Sprintf("health check failed (%d/%d): %s", attempt+2, checks, w.describe(res)))
	}
	w.Annotate(fmt.Sprintf("health check failed %d times in a row; stopping the instance as unhealthy", checks))
	return false
}

// check runs the probe once. done reports that ctx ended meanwhile: the probe
// was cut short by the instance going away, so its result means nothing.
func (w *Watcher) check(ctx context.Context) (res executor.ProbeResult, done bool) {
	res = w.Check(ctx)
	return res, ctx.Err() != nil
}

// passed applies the probe's own `failures` policy, exactly as for a task run:
// any outcome it doesn't classify as a failure is a pass.
func (w *Watcher) passed(res executor.ProbeResult) bool {
	return !w.Probe.IsFailureReason(res.EndReason(), res.ExitCode)
}

// describe renders one probe outcome for a log line: why it ended, then what
// it printed, folded onto that one line.
func (w *Watcher) describe(res executor.ProbeResult) string {
	var outcome string
	switch {
	case res.Error != nil:
		outcome = res.Error.Error()
	case res.EndReason() == model.ReasonTimeout:
		outcome = fmt.Sprintf("timed out after %s", w.Probe.TimeoutValue())
	default:
		outcome = fmt.Sprintf("exit %d", res.ExitCode)
	}
	if output := strings.Join(strings.Fields(res.Output), " "); output != "" {
		return outcome + ": " + output
	}
	return outcome
}

// Sleep is the production Watcher.Sleep: a context-aware wait.
func Sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}
