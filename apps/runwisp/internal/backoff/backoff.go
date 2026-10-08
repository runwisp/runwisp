// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package backoff computes jittered exponential retry delays. It only produces
// delays; each caller owns its own retry loop and decides when to give up.
package backoff

import (
	"math/rand/v2"
	"time"
)

// Exponential yields Initial, Initial*Multiplier, ... capped at Max, each
// randomized by ±Jitter (a fraction of the delay, 0 for none).
type Exponential struct {
	Initial    time.Duration
	Max        time.Duration
	Multiplier float64
	Jitter     float64

	current time.Duration
}

// Next returns the delay before the next attempt and advances the curve.
func (e *Exponential) Next() time.Duration {
	if e.current == 0 {
		e.current = e.Initial
	}
	d := e.current
	e.current = min(time.Duration(float64(e.current)*e.Multiplier), e.Max)
	spread := e.Jitter * (2*rand.Float64() - 1) // NOSONAR: retry jitter, not security
	return time.Duration(float64(d) * (1 + spread))
}

// Reset restarts the curve at Initial.
func (e *Exponential) Reset() {
	e.current = 0
}
