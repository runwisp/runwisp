// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package backoff

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestExponential_GrowsCapsAndResets(t *testing.T) {
	e := Exponential{Initial: time.Second, Max: 5 * time.Second, Multiplier: 2}
	var got []time.Duration
	for range 5 {
		got = append(got, e.Next())
	}
	assert.Equal(t, []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second}, got)

	e.Reset()
	assert.Equal(t, time.Second, e.Next())
}

func TestExponential_JitterStaysInRange(t *testing.T) {
	e := Exponential{Initial: time.Second, Max: time.Second, Multiplier: 1, Jitter: 0.2}
	for range 100 {
		d := e.Next()
		assert.GreaterOrEqual(t, d, 800*time.Millisecond)
		assert.LessOrEqual(t, d, 1200*time.Millisecond)
	}
}
