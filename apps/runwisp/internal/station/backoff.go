// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/backoff"
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

// newReconnectBackoff never gives up: the daemon keeps reconnecting for as
// long as it runs.
func newReconnectBackoff() *backoff.Exponential {
	return &backoff.Exponential{
		Initial:    reconnectBaseDelay,
		Max:        reconnectMaxDelay,
		Multiplier: reconnectMultiplier,
		Jitter:     reconnectJitterFraction,
	}
}
