// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package station

import (
	"runtime"

	"golang.org/x/sys/unix"
)

// lowerThreadPriority moves the calling goroutine onto a thread of its own in
// the background band, which lowers its CPU priority and throttles its disk
// reads. The goroutine never unlocks the thread, so the thread exits with it
// rather than lending the band to other goroutines. Best effort: a refusal
// changes nothing.
func lowerThreadPriority() {
	runtime.LockOSThread()
	_ = unix.Setpriority(unix.PRIO_DARWIN_THREAD, 0, unix.PRIO_DARWIN_BG)
}
