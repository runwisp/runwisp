// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
	canonical, ok := model.NormalizeSignalName(name)
	if !ok {
		return syscall.SIGTERM
	}
	switch canonical {
	case "SIGTERM":
		return syscall.SIGTERM
	case "SIGINT":
		return syscall.SIGINT
	case "SIGQUIT":
		return syscall.SIGQUIT
	case "SIGHUP":
		return syscall.SIGHUP
	case "SIGKILL":
		return syscall.SIGKILL
	case "SIGUSR1":
		return syscall.SIGUSR1
	case "SIGUSR2":
		return syscall.SIGUSR2
	default:
		return syscall.SIGTERM
	}
}
