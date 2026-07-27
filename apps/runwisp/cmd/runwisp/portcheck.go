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
	if host == "" {
		host = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return err
	}
	return ln.Close()
}

// portConflictError builds the user-facing message shown when something
// other than a RunWisp daemon is holding the configured port.
func portConflictError(host string, port int, cause error) error {
	displayHost := host
	if displayHost == "" {
		displayHost = "127.0.0.1"
	}
	return &userFacingError{
		title: fmt.Sprintf("port %d on %s is already in use by another process (%v)", port, displayHost, cause),
		details: "RunWisp could not start because something else is already listening on this port.\n" +
			"The process there did not respond to the RunWisp health check, so it does not appear to be a RunWisp daemon.\n\n" +
			"To resolve this you can:\n" +
			fmt.Sprintf("  - Stop the other process and try again\n"+
				"  - Run RunWisp on a different port:  runwisp --port <PORT>\n"+
				"  - Identify the culprit with:        ss -ltnp 'sport = :%d'   (or 'lsof -iTCP:%d -sTCP:LISTEN')",
				port, port),
	}
}
