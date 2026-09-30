// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package health

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime/retry"
)

var t0 = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// harness drives a Watcher on a fake clock: Sleep advances it instantly, each
// check takes probeTime, and check i returns script[i] (a pass once the script
// runs out). cancelAt cancels ctx during that check (0-based), as the run
// exiting mid-probe would.
type harness struct {
	t         *testing.T
	now       time.Time
	probeTime time.Duration
	schedule  cron.Schedule
	script    []bool
	cancelAt  int

	checks      []time.Duration // offset from t0 at which each check started
	sleeps      []time.Duration
	notes       []string
	healthyHits int
}

func newHarness(t *testing.T, script ...bool) *harness {
	return &harness{t: t, now: t0, script: script, cancelAt: -1, schedule: cron.ConstantDelaySchedule{Delay: 10 * time.Second}}
}

func (h *harness) watch(probe *model.Task, healthyAfter time.Duration) bool {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	w := &Watcher{
		Probe:        probe,
		Schedule:     h.schedule,
		Started:      t0,
		HealthyAfter: healthyAfter,
		Now:          func() time.Time { return h.now },
		Sleep: func(ctx context.Context, d time.Duration) bool {
			h.sleeps = append(h.sleeps, d)
			h.now = h.now.Add(d)
			return ctx.Err() == nil
		},
		Check: func(context.Context) executor.ProbeResult {
			i := len(h.checks)
			h.checks = append(h.checks, h.now.Sub(t0))
			h.now = h.now.Add(h.probeTime)
			if i == h.cancelAt {
				cancel()
				return executor.ProbeResult{} // exit 0: must not count as a pass
			}
			if i < len(h.script) && !h.script[i] {
				return executor.ProbeResult{ExecuteResult: executor.ExecuteResult{ExitCode: 1}, Output: "  connection\nrefused "}
			}
			return executor.ProbeResult{}
		},
		OnHealthy: func() { h.healthyHits++ },
		Annotate:  func(msg string) { h.notes = append(h.notes, msg) },
	}
	return w.Watch(ctx)
}

func (h *harness) noted(substr string) bool {
	for _, n := range h.notes {
		if strings.Contains(n, substr) {
			return true
		}
	}
	return false
}

func TestWatch_DeadlineMissed(t *testing.T) {
	h := newHarness(t, false, false, false)

	unhealthy := h.watch(&model.Task{}, 25*time.Second)

	assert.True(t, unhealthy)
	assert.Equal(t, []time.Duration{10 * time.Second, 20 * time.Second}, h.checks, "no probe starts at or after the deadline")
	assert.Equal(t, t0.Add(25*time.Second), h.now, "gives up exactly at the deadline")
	assert.Zero(t, h.healthyHits)
	assert.True(t, h.noted("ignored until it first passes: exit 1: connection refused"), h.notes)
	assert.True(t, h.noted("did not pass within healthy_after (25s)"), h.notes)
}

func TestWatch_PrePassFailuresIgnored_OnHealthyOnce(t *testing.T) {
	h := newHarness(t, false, true, true)
	h.cancelAt = 3

	unhealthy := h.watch(&model.Task{}, time.Minute)

	assert.False(t, unhealthy, "ctx ending is not a verdict")
	assert.Equal(t, 1, h.healthyHits)
	assert.Len(t, h.checks, 4)
	assert.True(t, h.noted("the instance is healthy"), h.notes)
}

func TestWatch_RetryChainExhausted(t *testing.T) {
	probe := &model.Task{RetryAttempts: 2, RetryDelay: ptr(3 * time.Second), RetryBackoff: model.BackoffExponential}
	h := newHarness(t, true, false, false, false)

	unhealthy := h.watch(probe, time.Minute)

	require.True(t, unhealthy)
	assert.Len(t, h.checks, 4)
	assert.Equal(t, []time.Duration{
		10 * time.Second, 10 * time.Second,
		retry.ComputeRetryDelay(probe, 0), retry.ComputeRetryDelay(probe, 1),
	}, h.sleeps)
	assert.True(t, h.noted("health check failed (1/3): exit 1"), h.notes)
	assert.True(t, h.noted("health check failed (3/3)"), h.notes)
	assert.True(t, h.noted("failed 3 times in a row"), h.notes)
}

func TestWatch_ZeroRetriesFailsOnFirstFailedCheck(t *testing.T) {
	h := newHarness(t, true, false)

	assert.True(t, h.watch(&model.Task{}, time.Minute))
	assert.Len(t, h.checks, 2)
}

func TestWatch_RetryPassRecovers(t *testing.T) {
	probe := &model.Task{RetryAttempts: 2, RetryDelay: ptr(time.Second)}
	h := newHarness(t, true, false, true, false, false, false)

	unhealthy := h.watch(probe, time.Minute)

	assert.True(t, unhealthy, "a fresh chain after the recovery runs out again")
	assert.Len(t, h.checks, 6)
	assert.Equal(t, 1, h.healthyHits, "recovering is not a second first pass")
	assert.True(t, h.noted("passed again"), h.notes)
}

func TestWatch_TicksDuringAProbeAreSkipped(t *testing.T) {
	grid, err := cron.NewParser(cron.Second | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow).Parse("*/10 * * * * *")
	require.NoError(t, err)
	h := newHarness(t, true, true)
	h.schedule = grid
	h.probeTime = 25 * time.Second
	h.cancelAt = 2

	h.watch(&model.Task{}, time.Minute)

	assert.Equal(t, []time.Duration{10 * time.Second, 40 * time.Second, 70 * time.Second}, h.checks)
}

func TestWatch_CancelMidProbeIsNotAPass(t *testing.T) {
	h := newHarness(t)
	h.cancelAt = 0

	assert.False(t, h.watch(&model.Task{}, time.Minute))
	assert.Zero(t, h.healthyHits)
}

func TestWatch_HonorsProbeFailuresPolicy(t *testing.T) {
	// failures = ["timeout"]: a non-zero exit is not a failure, so it passes.
	probe := &model.Task{Failures: model.FailureMatcher{Reasons: map[model.EndReason]struct{}{model.ReasonTimeout: {}}}}
	h := newHarness(t, false)
	h.cancelAt = 1

	h.watch(probe, time.Minute)

	assert.Equal(t, 1, h.healthyHits)
}

func TestDescribe(t *testing.T) {
	w := &Watcher{Probe: &model.Task{Timeout: ptr(5 * time.Second)}}
	timedOut := executor.ProbeResult{ExecuteResult: executor.ExecuteResult{ExitCode: -1, TimedOut: true}}
	assert.Equal(t, "timed out after 5s", w.describe(timedOut))
	assert.Equal(t, "exit 2", w.describe(executor.ProbeResult{ExecuteResult: executor.ExecuteResult{ExitCode: 2}}))
	matched := executor.ProbeResult{ExecuteResult: executor.ExecuteResult{OutputMatched: true}, MatchedPattern: "(?i)error"}
	assert.Equal(t, `exit 0, output matched failures pattern "(?i)error"`, w.describe(matched))
}

func ptr(d time.Duration) *time.Duration { return &d }
