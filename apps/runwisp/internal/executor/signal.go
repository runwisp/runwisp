// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"syscall"

	"github.com/runwisp/runwisp/internal/model"
)

// signalFromName resolves a stop_signal name to the syscall.Signal that opens
// the stop ladder. Config defaulting hands us a canonical "SIGxxx" name and
// validation has already rejected anything off the allowlist, so an unknown or
// empty value here just means a unit was constructed directly — fall back to
// SIGTERM rather than panic. SIGKILL is accepted and means "skip the graceful
// phase"; startCmd special-cases it.
func signalFromName(name string) syscall.Signal {
	canonical, _ := model.NormalizeSignalName(name)
	if sig, ok := signalsByName[canonical]; ok {
		return sig
	}
	return syscall.SIGTERM
}

var signalsByName = map[string]syscall.Signal{
	"SIGTERM": syscall.SIGTERM,
	"SIGINT":  syscall.SIGINT,
	"SIGQUIT": syscall.SIGQUIT,
	"SIGHUP":  syscall.SIGHUP,
	"SIGKILL": syscall.SIGKILL,
	"SIGUSR1": syscall.SIGUSR1,
	"SIGUSR2": syscall.SIGUSR2,
}
