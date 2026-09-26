// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package retry

import (
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

// durPtr returns a pointer to d — for building *time.Duration task fields
// (RestartDelay) in struct literals.
func durPtr(d time.Duration) *time.Duration { return &d }

func TestIsFailedExecution(t *testing.T) {
	for _, tc := range []struct {
		reason model.EndReason
		want   bool
	}{
		{model.ReasonFailed, true},
		{model.ReasonTimeout, true},
		{model.ReasonCrashed, true},
		{model.ReasonLogOverflow, true},
		{model.ReasonStartFailed, true},
		{model.ReasonSuccess, false},
		{model.ReasonStopped, false},
		{model.ReasonSkipped, false},
		{model.ReasonMissed, false},
	} {
		assert.Equalf(t, tc.want, IsFailedExecution(tc.reason),
			"IsFailedExecution(%q)", tc.reason)
	}
}

func TestShouldRestart(t *testing.T) {
	stopped := model.ReasonStopped
	failed := model.ReasonFailed
	success := model.ReasonSuccess
	timeout := model.ReasonTimeout
	crashed := model.ReasonCrashed

	t.Run("never policy never restarts", func(t *testing.T) {
		task := &model.Task{Restart: model.RestartNever}
		assert.False(t, ShouldRestart(task, &model.Run{EndReason: &failed}))
		assert.False(t, ShouldRestart(task, &model.Run{EndReason: &success}))
	})

	t.Run("empty policy never restarts", func(t *testing.T) {
		task := &model.Task{Restart: ""}
		assert.False(t, ShouldRestart(task, &model.Run{EndReason: &failed}))
	})

	t.Run("always policy on service always restarts", func(t *testing.T) {
		task := &model.Task{Restart: model.RestartAlways, Kind: model.KindService}
		assert.True(t, ShouldRestart(task, &model.Run{EndReason: &stopped}),
			"service must self-heal even after manual stop")
		assert.True(t, ShouldRestart(task, &model.Run{EndReason: &failed}))
		assert.True(t, ShouldRestart(task, &model.Run{EndReason: &success}))
	})

	t.Run("on_failure restarts only on failure reasons", func(t *testing.T) {
		task := &model.Task{Restart: model.RestartOnFailure}
		assert.True(t, ShouldRestart(task, &model.Run{EndReason: &failed}))
		assert.True(t, ShouldRestart(task, &model.Run{EndReason: &timeout}))
		assert.True(t, ShouldRestart(task, &model.Run{EndReason: &crashed}))
		assert.False(t, ShouldRestart(task, &model.Run{EndReason: &success}))
		assert.False(t, ShouldRestart(task, &model.Run{EndReason: &stopped}))
		assert.False(t, ShouldRestart(task, &model.Run{EndReason: nil}))
	})

	// Bug-first regression: on_failure must also respect the service's own
	// `failures` policy, not just the fixed IsFailedExecution ceiling — a
	// service that narrowed `failures` to a specific exit-code range must not
	// restart on an exit code outside that range.
	t.Run("on_failure narrows to the task's failures exit ranges", func(t *testing.T) {
		task := &model.Task{
			Restart: model.RestartOnFailure,
			Failures: model.FailureMatcher{
				Reasons:    map[model.EndReason]struct{}{},
				ExitRanges: [][2]int{{1, 23}},
			},
		}
		assert.False(t, ShouldRestart(task, &model.Run{EndReason: &failed, ExitCode: 42}),
			"exit 42 is outside the configured failure range")
		assert.True(t, ShouldRestart(task, &model.Run{EndReason: &failed, ExitCode: 7}))
	})

	t.Run("on_failure narrows when failures drops a reason", func(t *testing.T) {
		logOverflow := model.ReasonLogOverflow
		task := &model.Task{
			Restart: model.RestartOnFailure,
			Failures: model.FailureMatcher{Reasons: map[model.EndReason]struct{}{
				model.ReasonFailed: {},
			}},
		}
		assert.False(t, ShouldRestart(task, &model.Run{EndReason: &logOverflow}),
			"log_overflow was dropped from this task's failures list")
	})

	t.Run("on_failure never restarts on a promoted reason outside the ceiling", func(t *testing.T) {
		task := &model.Task{
			Restart: model.RestartOnFailure,
			Failures: model.FailureMatcher{Reasons: map[model.EndReason]struct{}{
				model.ReasonStopped: {},
			}},
		}
		assert.False(t, ShouldRestart(task, &model.Run{EndReason: &stopped}),
			"stopped is outside IsFailedExecution regardless of failures")
	})

	t.Run("on_failure with unconfigured failures behaves as before", func(t *testing.T) {
		task := &model.Task{Restart: model.RestartOnFailure} // nil Failures.Reasons
		assert.True(t, ShouldRestart(task, &model.Run{EndReason: &failed}))
		assert.True(t, ShouldRestart(task, &model.Run{EndReason: &timeout}))
		assert.True(t, ShouldRestart(task, &model.Run{EndReason: &crashed}))
	})
}

func TestShouldRetry(t *testing.T) {
	failed := model.ReasonFailed
	success := model.ReasonSuccess
	stopped := model.ReasonStopped

	t.Run("retry allowed on a failed run with attempts left", func(t *testing.T) {
		task := &model.Task{RetryAttempts: 3}
		assert.True(t, ShouldRetry(task, &model.Run{EndReason: &failed}))
	})

	t.Run("zero retry attempts disables retry", func(t *testing.T) {
		task := &model.Task{RetryAttempts: 0}
		assert.False(t, ShouldRetry(task, &model.Run{EndReason: &failed}))
	})

	t.Run("non-failure reasons skip retry", func(t *testing.T) {
		task := &model.Task{RetryAttempts: 3}
		assert.False(t, ShouldRetry(task, &model.Run{EndReason: &success}))
		assert.False(t, ShouldRetry(task, &model.Run{EndReason: &stopped}))
		assert.False(t, ShouldRetry(task, &model.Run{EndReason: nil}))
	})

	t.Run("retry budget boundary", func(t *testing.T) {
		task := &model.Task{RetryAttempts: 3}
		assert.True(t, ShouldRetry(task, &model.Run{EndReason: &failed, RetryAttempt: 0}))
		assert.True(t, ShouldRetry(task, &model.Run{EndReason: &failed, RetryAttempt: 2}))
		assert.False(t, ShouldRetry(task, &model.Run{EndReason: &failed, RetryAttempt: 3}),
			"attempt == max budget exhausts retry")
		assert.False(t, ShouldRetry(task, &model.Run{EndReason: &failed, RetryAttempt: 4}))
	})

	// Bug-first regression: retry_attempts must respect the task's own
	// `failures` policy, not just the fixed IsFailedExecution ceiling — a task
	// that narrowed `failures` to a specific exit-code range must not burn its
	// retry budget on an exit code outside that range.
	t.Run("retry narrows to the task's failures exit ranges", func(t *testing.T) {
		task := &model.Task{
			RetryAttempts: 3,
			Failures: model.FailureMatcher{
				Reasons:    map[model.EndReason]struct{}{},
				ExitRanges: [][2]int{{1, 23}},
			},
		}
		assert.False(t, ShouldRetry(task, &model.Run{EndReason: &failed, ExitCode: 42}),
			"exit 42 is outside the configured failure range")
		assert.True(t, ShouldRetry(task, &model.Run{EndReason: &failed, ExitCode: 7}))
	})

	t.Run("retry narrows when failures drops a reason", func(t *testing.T) {
		logOverflow := model.ReasonLogOverflow
		task := &model.Task{
			RetryAttempts: 3,
			Failures: model.FailureMatcher{Reasons: map[model.EndReason]struct{}{
				model.ReasonFailed: {},
			}},
		}
		assert.False(t, ShouldRetry(task, &model.Run{EndReason: &logOverflow}),
			"log_overflow was dropped from this task's failures list")
	})

	t.Run("retry never fires on a promoted reason outside the ceiling", func(t *testing.T) {
		task := &model.Task{
			RetryAttempts: 3,
			Failures: model.FailureMatcher{Reasons: map[model.EndReason]struct{}{
				model.ReasonStopped: {},
			}},
		}
		assert.False(t, ShouldRetry(task, &model.Run{EndReason: &stopped}),
			"stopped is outside IsFailedExecution regardless of failures")
	})

	t.Run("retry fires on an output-matched run even when failed is dropped", func(t *testing.T) {
		task := &model.Task{
			RetryAttempts: 3,
			Failures: model.FailureMatcher{
				Reasons:        map[model.EndReason]struct{}{},
				OutputPatterns: []string{"ERROR"},
			},
		}
		assert.True(t, ShouldRetry(task, &model.Run{EndReason: &failed, OutputMatched: true}))
		assert.False(t, ShouldRetry(task, &model.Run{EndReason: &failed, ExitCode: 1}))
	})

	t.Run("retry with unconfigured failures behaves as before", func(t *testing.T) {
		task := &model.Task{RetryAttempts: 3} // nil Failures.Reasons
		assert.True(t, ShouldRetry(task, &model.Run{EndReason: &failed}))
	})
}

func TestComputeRetryDelay(t *testing.T) {
	t.Run("default backoff returns base delay", func(t *testing.T) {
		task := &model.Task{RetryDelay: durPtr(2 * time.Second), RetryBackoff: ""}
		for attempt := 0; attempt < 5; attempt++ {
			assert.Equalf(t, 2*time.Second, ComputeRetryDelay(task, attempt),
				"empty backoff should stay constant (attempt %d)", attempt)
		}
	})

	t.Run("unknown backoff treated as default", func(t *testing.T) {
		task := &model.Task{RetryDelay: durPtr(time.Second), RetryBackoff: "fibonacci"}
		assert.Equal(t, time.Second, ComputeRetryDelay(task, 5))
	})

	t.Run("nil retry_delay falls back to the default", func(t *testing.T) {
		task := &model.Task{RetryDelay: nil, RetryBackoff: "exponential"}
		assert.Equal(t, config.DefaultRetryDelay, ComputeRetryDelay(task, 0))
	})

	t.Run("explicit zero retry_delay is honored (no delay)", func(t *testing.T) {
		task := &model.Task{RetryDelay: durPtr(0), RetryBackoff: "exponential"}
		assert.Equal(t, time.Duration(0), ComputeRetryDelay(task, 0))
		assert.Equal(t, time.Duration(0), ComputeRetryDelay(task, 5))
	})

	t.Run("exponential doubles each attempt", func(t *testing.T) {
		task := &model.Task{RetryDelay: durPtr(time.Second), RetryBackoff: "exponential"}
		assert.Equal(t, time.Second, ComputeRetryDelay(task, 0))
		assert.Equal(t, 2*time.Second, ComputeRetryDelay(task, 1))
		assert.Equal(t, 4*time.Second, ComputeRetryDelay(task, 2))
		assert.Equal(t, 8*time.Second, ComputeRetryDelay(task, 3))
	})

	t.Run("exponential clamps to five-minute max", func(t *testing.T) {
		task := &model.Task{RetryDelay: durPtr(time.Second), RetryBackoff: "exponential"}
		assert.Equal(t, 5*time.Minute, ComputeRetryDelay(task, 30))
		assert.Equal(t, 5*time.Minute, ComputeRetryDelay(task, 100))
	})

	t.Run("linear scales with attempt+1", func(t *testing.T) {
		task := &model.Task{RetryDelay: durPtr(2 * time.Second), RetryBackoff: "linear"}
		assert.Equal(t, 2*time.Second, ComputeRetryDelay(task, 0))
		assert.Equal(t, 4*time.Second, ComputeRetryDelay(task, 1))
		assert.Equal(t, 6*time.Second, ComputeRetryDelay(task, 2))
	})

	t.Run("linear clamps to five-minute max", func(t *testing.T) {
		task := &model.Task{RetryDelay: durPtr(time.Minute), RetryBackoff: "linear"}
		assert.Equal(t, 5*time.Minute, ComputeRetryDelay(task, 10))
	})
}

func TestRestartDelay(t *testing.T) {
	stopped := model.ReasonStopped
	crashed := model.ReasonCrashed

	t.Run("operator stop refills immediately regardless of backoff", func(t *testing.T) {
		task := &model.Task{
			RestartDelay:   durPtr(5 * time.Second),
			RestartBackoff: model.BackoffExponential,
		}
		for attempt := 0; attempt < 5; attempt++ {
			assert.Zerof(t, RestartDelay(task, attempt, &stopped),
				"operator stop must not wait (attempt %d)", attempt)
		}
	})

	t.Run("crash keeps the configured backoff", func(t *testing.T) {
		task := &model.Task{
			RestartDelay:   durPtr(time.Second),
			RestartBackoff: model.BackoffExponential,
		}
		assert.Equal(t, ComputeRestartDelay(task, 2), RestartDelay(task, 2, &crashed))
		assert.Equal(t, ComputeRestartDelay(task, 2), RestartDelay(task, 2, nil))
	})
}

func TestComputeRestartDelay(t *testing.T) {
	t.Run("first attempt returns base delay", func(t *testing.T) {
		task := &model.Task{
			RestartDelay:   durPtr(time.Second),
			RestartBackoff: model.BackoffExponential,
		}
		assert.Equal(t, time.Second, ComputeRestartDelay(task, 0))
	})

	t.Run("constant backoff stays at base", func(t *testing.T) {
		task := &model.Task{
			RestartDelay:   durPtr(500 * time.Millisecond),
			RestartBackoff: model.BackoffConstant,
		}
		for attempt := 0; attempt < 10; attempt++ {
			assert.Equalf(t, 500*time.Millisecond, ComputeRestartDelay(task, attempt),
				"attempt %d should stay at base delay", attempt)
		}
	})

	t.Run("empty backoff stays at base", func(t *testing.T) {
		task := &model.Task{
			RestartDelay:   durPtr(750 * time.Millisecond),
			RestartBackoff: "",
		}
		assert.Equal(t, 750*time.Millisecond, ComputeRestartDelay(task, 5))
	})

	t.Run("exponential doubles each attempt up to cap", func(t *testing.T) {
		task := &model.Task{
			RestartDelay:   durPtr(time.Second),
			RestartBackoff: model.BackoffExponential,
		}
		want := []time.Duration{
			time.Second,       // attempt 0 → base
			2 * time.Second,   // 1
			4 * time.Second,   // 2
			8 * time.Second,   // 3
			16 * time.Second,  // 4
			32 * time.Second,  // 5
			RestartBackoffCap, // 6 → 64s clamped
			RestartBackoffCap, // 7
			RestartBackoffCap, // 8
		}
		for attempt, expected := range want {
			assert.Equalf(t, expected, ComputeRestartDelay(task, attempt),
				"attempt %d", attempt)
		}
	})

	t.Run("nil restart delay falls back to the built-in default", func(t *testing.T) {
		task := &model.Task{
			RestartBackoff: model.BackoffExponential,
		}
		assert.Equal(t, config.DefaultRestartDelay, ComputeRestartDelay(task, 0))
	})

	// Bug-first regression: an explicit restart_delay = "0s" must be preserved
	// literally (restart instantly), not silently defaulted back up — including
	// through backoff math at later attempts, where a naive "delay <= 0 means
	// unset" guard would have clamped it to the cap instead of keeping it zero.
	t.Run("explicit zero restart delay is preserved through backoff", func(t *testing.T) {
		task := &model.Task{
			RestartDelay:   durPtr(0),
			RestartBackoff: model.BackoffExponential,
		}
		assert.Equal(t, time.Duration(0), ComputeRestartDelay(task, 0))
		assert.Equal(t, time.Duration(0), ComputeRestartDelay(task, 5))
	})

	t.Run("large attempt clamps to cap", func(t *testing.T) {
		task := &model.Task{
			RestartDelay:   durPtr(time.Second),
			RestartBackoff: model.BackoffExponential,
		}
		assert.Equal(t, RestartBackoffCap, ComputeRestartDelay(task, 100))
	})

	t.Run("linear scales with attempt+1", func(t *testing.T) {
		task := &model.Task{
			RestartDelay:   durPtr(2 * time.Second),
			RestartBackoff: model.BackoffLinear,
		}
		// delay = base * (attempt+1)
		assert.Equal(t, 4*time.Second, ComputeRestartDelay(task, 1)) // 2s * 2
		assert.Equal(t, 6*time.Second, ComputeRestartDelay(task, 2)) // 2s * 3
		assert.Equal(t, 8*time.Second, ComputeRestartDelay(task, 3)) // 2s * 4
	})

	t.Run("linear clamps to cap", func(t *testing.T) {
		task := &model.Task{
			RestartDelay:   durPtr(time.Minute),
			RestartBackoff: model.BackoffLinear,
		}
		assert.Equal(t, RestartBackoffCap, ComputeRestartDelay(task, 10))
	})
}
