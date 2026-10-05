// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package bootid

import "golang.org/x/sys/unix"

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
