// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package station

import (
	"runtime"

	"golang.org/x/sys/unix"
)

// grepNice is the nice value of a log:search's thread. A nicer thread also gets
// a lower disk priority from the schedulers that keep one (BFQ).
const grepNice = 10

// lowerThreadPriority moves the calling goroutine onto a thread of its own at
// nice grepNice, below the daemon and the runs it starts. The goroutine never
// unlocks the thread, so the thread exits with it rather than lending the
// priority to other goroutines. Best effort: a refusal changes nothing.
func lowerThreadPriority() {
	runtime.LockOSThread()
	_ = unix.Setpriority(unix.PRIO_PROCESS, unix.Gettid(), grepNice)
}
