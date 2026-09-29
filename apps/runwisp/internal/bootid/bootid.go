// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package bootid names the current machine or container boot, so a
// run_on_start = "boot" task can tell a reboot from a daemon restart.
//
// Current returns a string that stays the same for the life of one boot and
// changes on the next, or "" when the platform exposes no boot identity (the
// caller then falls back to firing on every daemon start).
package bootid

import (
	"strings"
)

// fromProc builds the Linux boot identity from the kernel boot_id and the start
// time of PID 1.
//
// boot_id changes on every kernel boot but is not namespaced, so inside a
// container it is the host's. PID 1 is the init of this PID namespace: the
// host's init on a bare machine (started once per boot, so it adds nothing),
// and the container's entrypoint in a container, which a fresh `docker run`
// or a `docker restart` replaces. Its start time (clock ticks since kernel
// boot) therefore marks "this container started", and pairing it with boot_id
// keeps it unique across host reboots. That makes a container start count as
// a boot without having to detect containers at all. Container IDs from
// cgroup/mountinfo were rejected: cgroup v2 hides them ("0::/"), the mountinfo
// layout differs per runtime, and they survive `docker restart`.
//
// When /proc/1/stat is unreadable (hidepid) boot_id alone is used; that is
// right on a host and only wrong in a container mounting /proc with hidepid.
func fromProc(readFile func(string) ([]byte, error)) string {
	raw, err := readFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return ""
	}
	id := strings.TrimSpace(string(raw))
	if id == "" {
		return ""
	}
	if stat, err := readFile("/proc/1/stat"); err == nil {
		if start := statStartTime(string(stat)); start != "" {
			id += "/" + start
		}
	}
	return id
}

// statStartTime returns field 22 (starttime) of a /proc/<pid>/stat line. The
// comm field (2) is parenthesised and may itself hold spaces or ')', so fields
// are counted from the last ')': state is field 3, so starttime is the 20th
// field after it.
func statStartTime(stat string) string {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return ""
	}
	fields := strings.Fields(stat[i+1:])
	if len(fields) < 20 {
		return ""
	}
	return fields[19]
}
