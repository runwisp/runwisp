// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cloud

import (
	"time"

	"github.com/cenkalti/backoff/v4"
)

const (
	reconnectBaseDelay      = 500 * time.Millisecond
	reconnectMaxDelay       = 30 * time.Second
	reconnectMultiplier     = 2.0
	reconnectJitterFraction = 0.20
	// minStableSessionDuration is how long a session must stay up before a clean
	// reconnect resets the backoff. A session shorter than this is treated as a
	// flap, so the backoff keeps escalating instead of resetting every attempt.
	// One full max-delay window is comfortably past the reconnect churn.
	minStableSessionDuration = reconnectMaxDelay
)

func newReconnectBackoff() backoff.BackOff {
	b := backoff.NewExponentialBackOff()
	b.InitialInterval = reconnectBaseDelay
	b.MaxInterval = reconnectMaxDelay
	b.Multiplier = reconnectMultiplier
	b.RandomizationFactor = reconnectJitterFraction
	b.MaxElapsedTime = 0 // Never stop reconnecting
	return b
}
