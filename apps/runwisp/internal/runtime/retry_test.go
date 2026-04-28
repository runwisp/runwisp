// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

func TestComputeRestartDelay(t *testing.T) {
	t.Run("first attempt returns base delay", func(t *testing.T) {
		task := &model.Task{
			RestartDelay:   "1s",
			RestartBackoff: model.RestartBackoffExponential,
		}
		assert.Equal(t, time.Second, computeRestartDelay(task, 0))
	})

	t.Run("none backoff stays at base", func(t *testing.T) {
		task := &model.Task{
			RestartDelay:   "500ms",
			RestartBackoff: model.RestartBackoffNone,
		}
		for attempt := 0; attempt < 10; attempt++ {
			assert.Equalf(t, 500*time.Millisecond, computeRestartDelay(task, attempt),
				"attempt %d should stay at base delay", attempt)
		}
	})

	t.Run("empty backoff stays at base", func(t *testing.T) {
		task := &model.Task{
			RestartDelay:   "750ms",
			RestartBackoff: "",
		}
		assert.Equal(t, 750*time.Millisecond, computeRestartDelay(task, 5))
	})

	t.Run("exponential doubles each attempt up to cap", func(t *testing.T) {
		task := &model.Task{
			RestartDelay:   "1s",
			RestartBackoff: model.RestartBackoffExponential,
		}
		want := []time.Duration{
			time.Second,       // attempt 0 → base
			2 * time.Second,   // 1
			4 * time.Second,   // 2
			8 * time.Second,   // 3
			16 * time.Second,  // 4
			32 * time.Second,  // 5
			restartBackoffCap, // 6 → 64s clamped
			restartBackoffCap, // 7
			restartBackoffCap, // 8
		}
		for attempt, expected := range want {
			assert.Equalf(t, expected, computeRestartDelay(task, attempt),
				"attempt %d", attempt)
		}
	})

	t.Run("default base delay when unparseable", func(t *testing.T) {
		task := &model.Task{
			RestartDelay:   "garbage",
			RestartBackoff: model.RestartBackoffExponential,
		}
		// fallback is 1 second
		assert.Equal(t, time.Second, computeRestartDelay(task, 0))
	})

	t.Run("large attempt clamps to cap", func(t *testing.T) {
		task := &model.Task{
			RestartDelay:   "1s",
			RestartBackoff: model.RestartBackoffExponential,
		}
		assert.Equal(t, restartBackoffCap, computeRestartDelay(task, 100))
	})
}
