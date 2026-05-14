// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/runwisp/runwisp/internal/datadir"
)

// localAPIBaseURL returns the loopback TCP URL the browser uses. Local CLI
// commands now talk over the Unix socket; only the browser flow (and any
// future remote --host override) needs this.
func localAPIBaseURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", flags.Port)
}

// localAPISocketPath returns the Unix socket path inside the configured data dir.
func localAPISocketPath() string {
	return datadir.SocketPath(flags.DataDir)
}
