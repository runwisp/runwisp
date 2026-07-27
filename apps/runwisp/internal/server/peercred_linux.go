// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package server

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// peerUID returns the UID of the process on the other end of c. It is a
// belt-and-suspenders check on top of the 0600 socket-file perm: even if the
// permissions are accidentally relaxed, this guarantees only the daemon's own
// UID can drive privileged operations through the local-trusted bypass.
func peerUID(c *net.UnixConn) (uint32, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("unix syscall conn: %w", err)
	}
	var (
		cred    *unix.Ucred
		credErr error
	)
	ctrlErr := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if ctrlErr != nil {
		return 0, fmt.Errorf("control fd: %w", ctrlErr)
	}
	if credErr != nil {
		return 0, fmt.Errorf("getsockopt SO_PEERCRED: %w", credErr)
	}
	return cred.Uid, nil
}
