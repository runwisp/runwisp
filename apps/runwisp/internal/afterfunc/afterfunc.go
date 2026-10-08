// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package afterfunc is the injectable timer seam shared by components that
// arm cancellable one-shot timers (the jitter gate, notification coalescing).
package afterfunc

import "time"

// Stopper cancels a not-yet-fired timer.
type Stopper interface {
	Stop() bool
}

// Func schedules fn after d and returns a handle to cancel it.
type Func func(d time.Duration, fn func()) Stopper

// Real is the production Func, backed by time.AfterFunc.
func Real(d time.Duration, fn func()) Stopper { return time.AfterFunc(d, fn) }
