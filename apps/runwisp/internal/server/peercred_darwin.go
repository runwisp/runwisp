// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package server

import (
	"fmt"
	"net"

	"golang.org/x/sys/unix"
)

// peerUID returns the UID of the process on the other end of c. macOS exposes
// the credential via LOCAL_PEERCRED on SOL_LOCAL, returning an Xucred whose
// Uid field is the effective UID at the time of connect.
func peerUID(c *net.UnixConn) (uint32, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("unix syscall conn: %w", err)
	}
	var (
		cred    *unix.Xucred
		credErr error
	)
	ctrlErr := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	})
	if ctrlErr != nil {
		return 0, fmt.Errorf("control fd: %w", ctrlErr)
	}
	if credErr != nil {
		return 0, fmt.Errorf("getsockopt LOCAL_PEERCRED: %w", credErr)
	}
	return cred.Uid, nil
}
