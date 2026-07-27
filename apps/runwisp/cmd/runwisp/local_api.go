// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"github.com/runwisp/runwisp/internal/datadir"
)

// localAPISocketPath resolves the daemon control socket: the explicit --socket
// / RUNWISP_SOCKET path when set, otherwise the default inside the data dir.
// Every CLI subcommand routes through here, so --socket lets them reach a
// daemon without also restating --data.
func localAPISocketPath(f Flags) string {
	if f.Socket != "" {
		return f.Socket
	}
	return datadir.SocketPath(f.DataDir)
}
