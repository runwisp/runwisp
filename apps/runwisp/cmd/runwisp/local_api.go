// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"github.com/runwisp/runwisp/internal/datadir"
)

// localAPISocketPath returns the Unix socket path inside the configured data dir.
func localAPISocketPath() string {
	return datadir.SocketPath(flags.DataDir)
}
