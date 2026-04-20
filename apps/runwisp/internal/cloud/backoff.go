// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
