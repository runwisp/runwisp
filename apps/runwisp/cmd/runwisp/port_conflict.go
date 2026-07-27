// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server"
)

// conflictChoice is the launcher's decision after probing an occupied port.
type conflictChoice int

const (
	// conflictAbort: give up. The accompanying error explains why (a generic
	// port conflict, an identity-rich message, or nil when the operator quit).
	conflictAbort conflictChoice = iota
	// conflictConnect: attach the TUI to the discovered daemon over its socket.
	conflictConnect
	// conflictStopAndLaunch: the operator chose to stop the discovered daemon;
	// the launcher should do so and then spawn its own daemon here.
	conflictStopAndLaunch
)

// probeRunwispInstance asks whoever holds host:port for its RunWisp identity.
// It returns the identity when the port-holder is a RunWisp daemon reachable
// over loopback, or nil for everything else: a non-RunWisp process, a RunWisp
// daemon bound beyond loopback (403, paths withheld), or an unreachable/slow
// listener. Best-effort by design — it never returns an error.
func probeRunwispInstance(host string, port int) *model.InstanceInfo {
	if host == "" {
		host = "127.0.0.1"
	}
	client := apiclient.NewProbe(fmt.Sprintf("http://%s:%d", host, port))
	info, err := client.GetInstanceInfo()
	if err != nil || info == nil || info.App != server.AppName {
		return nil
	}
	return info
}

// portConflictMessage builds the best available error for an occupied port:
// the identity-rich runwispPortConflictError when a RunWisp daemon holds it, or
// the generic portConflictError otherwise.
func portConflictMessage(host string, port int, bindErr error, info *model.InstanceInfo) error {
	if info == nil {
		return portConflictError(host, port, bindErr)
	}
	return runwispPortConflictError(host, port, info)
}

// nonInteractivePortConflict probes the port-holder and returns the best error
// for callers that have no operator to prompt (background daemon spawn, service
// install).
func nonInteractivePortConflict(host string, port int, bindErr error) error {
	return portConflictMessage(host, port, bindErr, probeRunwispInstance(host, port))
}

// resolvePortConflict decides what to do when host:port is already taken. info
// is the identity of the RunWisp daemon holding it, or nil when the port-holder
// is not an identifiable RunWisp daemon.
//
//   - nil info, or non-interactive → conflictAbort with portConflictMessage.
//   - interactive → print the identity and prompt the operator to [c]onnect,
//     [s]top and launch here, or [q]uit.
func resolvePortConflict(f Flags, bindErr error, info *model.InstanceInfo, interactive bool, in io.Reader, out io.Writer) (conflictChoice, error) {
	if info == nil || !interactive {
		return conflictAbort, portConflictMessage(f.Host, f.Port, bindErr, info)
	}

	fmt.Fprintf(out, "A RunWisp daemon (v%s, pid %d) is already running on port %d.\n", info.Version, info.Pid, f.Port)
	fmt.Fprintln(out, "It was started from a different data directory:")
	for _, line := range instanceSummaryLines(info) {
		fmt.Fprintln(out, line)
	}

	reader := bufio.NewReader(in)
	for {
		fmt.Fprint(out, "\nConnect to it, stop it and launch here, or quit? [c/s/q] ")
		answer, err := reader.ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "c", "connect":
			return conflictConnect, nil
		case "s", "stop":
			return conflictStopAndLaunch, nil
		case "q", "quit", "":
			return conflictAbort, nil
		default:
			fmt.Fprintln(out, "Please answer c, s, or q.")
		}
		// Treat a closed stdin (EOF with no newline) as "quit" so the prompt
		// can't spin forever when there's nothing left to read.
		if err != nil {
			if errors.Is(err, io.EOF) {
				return conflictAbort, nil
			}
			return conflictAbort, fmt.Errorf("read prompt response: %w", err)
		}
	}
}

// stopConflictingDaemon SIGTERMs the discovered daemon and waits for it to
// exit, reusing the same path as `runwisp stop` (PID file in its datadir).
func stopConflictingDaemon(f Flags, info *model.InstanceInfo) error {
	other := f
	other.DataDir = info.DataDir
	other.Socket = info.SocketPath
	return shutdownDaemon(other)
}
