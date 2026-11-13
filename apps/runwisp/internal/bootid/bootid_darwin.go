// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package bootid

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// Current returns this boot's identity: kern.bootsessionuuid, a UUID macOS
// generates once per boot. (kern.boottime shifts when the clock is set, which
// would read as a new boot.)
func Current() string {
	id, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return ""
	}
	return id
}

// Process returns the identity of process pid: the boot identity plus its
// start time from kern.proc.pid, or "" when it doesn't exist.
func Process(pid int) string {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || k.Proc.P_pid != int32(pid) {
		return ""
	}
	t := k.Proc.P_starttime
	return qualify(Current(), fmt.Sprintf("%d.%06d", t.Sec, t.Usec))
}
