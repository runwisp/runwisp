// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package retry

import (
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

func TestIsFailureReason(t *testing.T) {
	for _, tc := range []struct {
		reason model.EndReason
		want   bool
	}{
		{model.ReasonFailed, true},
		{model.ReasonTimeout, true},
		{model.ReasonCrashed, true},
		{model.ReasonSuccess, false},
		{model.ReasonStopped, false},
	} {
		assert.Equalf(t, tc.want, IsFailureReason(tc.reason),
			"IsFailureReason(%q)", tc.reason)
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

	t.Run("always policy restarts unless stopped (non-service)", func(t *testing.T) {
		task := &model.Task{Restart: model.RestartAlways}
		assert.True(t, ShouldRestart(task, &model.Run{EndReason: &failed}))
		assert.True(t, ShouldRestart(task, &model.Run{EndReason: &success}))
		assert.True(t, ShouldRestart(task, &model.Run{EndReason: nil}))
		assert.False(t, ShouldRestart(task, &model.Run{EndReason: &stopped}))
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
}

func TestShouldRetry(t *testing.T) {
	failed := model.ReasonFailed
	success := model.ReasonSuccess
	stopped := model.ReasonStopped

	t.Run("retry disabled when restart policy active", func(t *testing.T) {
		for _, policy := range []model.RestartPolicy{model.RestartAlways, model.RestartOnFailure} {
			task := &model.Task{Restart: policy, RetryAttempts: 3}
			assert.Falsef(t, ShouldRetry(task, &model.Run{EndReason: &failed}),
				"restart=%s should disable retry", policy)
		}
	})

	t.Run("retry allowed when restart never or empty", func(t *testing.T) {
		for _, policy := range []model.RestartPolicy{"", model.RestartNever} {
			task := &model.Task{Restart: policy, RetryAttempts: 3}
			assert.Truef(t, ShouldRetry(task, &model.Run{EndReason: &failed}),
				"restart=%q should allow retry", policy)
		}
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
}

func TestComputeRetryDelay(t *testing.T) {
	t.Run("default backoff returns base delay", func(t *testing.T) {
		task := &model.Task{RetryDelay: 2 * time.Second, RetryBackoff: ""}
		for attempt := 0; attempt < 5; attempt++ {
			assert.Equalf(t, 2*time.Second, ComputeRetryDelay(task, attempt),
				"empty backoff should stay constant (attempt %d)", attempt)
		}
	})

	t.Run("unknown backoff treated as default", func(t *testing.T) {
		task := &model.Task{RetryDelay: time.Second, RetryBackoff: "fibonacci"}
		assert.Equal(t, time.Second, ComputeRetryDelay(task, 5))
	})

	t.Run("zero base delay falls back to five seconds", func(t *testing.T) {
		task := &model.Task{RetryDelay: 0, RetryBackoff: "exponential"}
		assert.Equal(t, 5*time.Second, ComputeRetryDelay(task, 0))
	})

	t.Run("exponential doubles each attempt", func(t *testing.T) {
		task := &model.Task{RetryDelay: time.Second, RetryBackoff: "exponential"}
		assert.Equal(t, time.Second, ComputeRetryDelay(task, 0))
		assert.Equal(t, 2*time.Second, ComputeRetryDelay(task, 1))
		assert.Equal(t, 4*time.Second, ComputeRetryDelay(task, 2))
		assert.Equal(t, 8*time.Second, ComputeRetryDelay(task, 3))
	})

	t.Run("exponential clamps to five-minute max", func(t *testing.T) {
		task := &model.Task{RetryDelay: time.Second, RetryBackoff: "exponential"}
		assert.Equal(t, 5*time.Minute, ComputeRetryDelay(task, 30))
		assert.Equal(t, 5*time.Minute, ComputeRetryDelay(task, 100))
	})

	t.Run("linear scales with attempt+1", func(t *testing.T) {
		task := &model.Task{RetryDelay: 2 * time.Second, RetryBackoff: "linear"}
		assert.Equal(t, 2*time.Second, ComputeRetryDelay(task, 0))
		assert.Equal(t, 4*time.Second, ComputeRetryDelay(task, 1))
		assert.Equal(t, 6*time.Second, ComputeRetryDelay(task, 2))
	})

	t.Run("linear clamps to five-minute max", func(t *testing.T) {
		task := &model.Task{RetryDelay: time.Minute, RetryBackoff: "linear"}
		assert.Equal(t, 5*time.Minute, ComputeRetryDelay(task, 10))
	})
}

func TestComputeRestartDelay(t *testing.T) {
	t.Run("first attempt returns base delay", func(t *testing.T) {
		task := &model.Task{
			RestartDelay:   time.Second,
			RestartBackoff: model.RestartBackoffExponential,
		}
		assert.Equal(t, time.Second, ComputeRestartDelay(task, 0))
	})

	t.Run("none backoff stays at base", func(t *testing.T) {
		task := &model.Task{
			RestartDelay:   500 * time.Millisecond,
			RestartBackoff: model.RestartBackoffNone,
		}
		for attempt := 0; attempt < 10; attempt++ {
			assert.Equalf(t, 500*time.Millisecond, ComputeRestartDelay(task, attempt),
				"attempt %d should stay at base delay", attempt)
		}
	})

	t.Run("empty backoff stays at base", func(t *testing.T) {
		task := &model.Task{
			RestartDelay:   750 * time.Millisecond,
			RestartBackoff: "",
		}
		assert.Equal(t, 750*time.Millisecond, ComputeRestartDelay(task, 5))
	})

	t.Run("exponential doubles each attempt up to cap", func(t *testing.T) {
		task := &model.Task{
			RestartDelay:   time.Second,
			RestartBackoff: model.RestartBackoffExponential,
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

	t.Run("zero base delay falls back to one second", func(t *testing.T) {
		task := &model.Task{
			RestartDelay:   0,
			RestartBackoff: model.RestartBackoffExponential,
		}
		assert.Equal(t, time.Second, ComputeRestartDelay(task, 0))
	})

	t.Run("large attempt clamps to cap", func(t *testing.T) {
		task := &model.Task{
			RestartDelay:   time.Second,
			RestartBackoff: model.RestartBackoffExponential,
		}
		assert.Equal(t, RestartBackoffCap, ComputeRestartDelay(task, 100))
	})
}
