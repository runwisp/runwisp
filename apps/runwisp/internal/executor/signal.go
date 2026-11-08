// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"syscall"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/bootid"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
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

// StopOrphanGroup stops process group pgid, which a run started under a daemon
// that has since died, with task's stop ladder: its stop_signal, then SIGKILL
// once graceful_stop (capped at limit) has passed with the group still alive.
// A nil task (one removed from the config since) gets the default ladder. It acts only while the
// group's leader is still the process that run started: start is the leader's
// bootid.Process recorded at spawn. A gone leader, a reused pid or an unknown
// start leaves everything alone, so a stale row can never signal an unrelated
// process. Reports whether it signalled.
//
// The leader's pid can't be reused while it is alive, so a matching start
// means the group with that id is the run's own.
func StopOrphanGroup(pgid int, start string, task *model.Task, limit time.Duration) bool {
	if pgid <= 0 || start == "" || bootid.Process(pgid) != start {
		return false
	}
	stopSig, grace := syscall.SIGTERM, time.Duration(0)
	if task != nil {
		stopSig, grace = signalFromName(task.StopSignal), min(task.GracefulStopValue(), limit)
	}
	if grace > 0 && stopSig != syscall.SIGKILL {
		_ = syscall.Kill(-pgid, stopSig)
		for deadline := time.Now().Add(grace); groupAlive(pgid) && time.Now().Before(deadline); {
			time.Sleep(orphanPollInterval)
		}
		if !groupAlive(pgid) {
			return true
		}
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	return true
}

// orphanPollInterval is how often StopOrphanGroup checks whether the group has
// exited during its grace window.
const orphanPollInterval = 50 * time.Millisecond

func groupAlive(pgid int) bool { return syscall.Kill(-pgid, 0) == nil }
