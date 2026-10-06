// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"net"
)

// probePortAvailable reports whether we can bind to host:port right now.
// Returns nil if the port is free, or the bind error (typically EADDRINUSE).
// The listener is closed immediately on success.
func probePortAvailable(host string, port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", bindHost(host), port))
	if err != nil {
		return err
	}
	return ln.Close()
}

// bindHost fills in the loopback address RunWisp binds by default, so an
// empty --host still names something an operator can act on.
func bindHost(host string) string {
	if host == "" {
		return "127.0.0.1"
	}
	return host
}

// portConflictError builds the user-facing message shown when something
// other than a RunWisp daemon is holding the configured port.
func portConflictError(host string, port int, cause error) error {
	return &userFacingError{
		title: fmt.Sprintf("port %d on %s is already in use by another process (%v)", port, bindHost(host), cause),
		details: "RunWisp could not start because something else is already listening on this port.\n" +
			"The process there did not respond to the RunWisp health check, so it does not appear to be a RunWisp daemon.\n\n" +
			"To resolve this you can:\n" +
			fmt.Sprintf("  - Stop the other process and try again\n"+
				"  - Run RunWisp on a different port:  runwisp --port <PORT>\n"+
				"  - Identify the culprit with:        ss -ltnp 'sport = :%d'   (or 'lsof -iTCP:%d -sTCP:LISTEN')",
				port, port),
	}
}
