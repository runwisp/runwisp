// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"syscall"
)

// restartSignal asks a daemon that no service manager owns to restart in
// place: it shuts down gracefully, then main re-executes the binary under the
// same PID. `runwisp restart` sends it. The process never exits, so a Docker
// container (whose main process the daemon is) or the terminal it runs in
// keeps going.
const restartSignal = syscall.SIGUSR2

// errRestartInPlace is what runDaemon returns once a restart-in-place shutdown
// has finished. main re-executes the binary on it; handleCLIError prints
// nothing.
var errRestartInPlace = errors.New("restart in place")

// requestRestart marks the daemon for a restart in place. It refuses (logging
// why) when the binary to re-execute can't be found, so the daemon keeps
// running instead of shutting down for good.
func (rt *daemonRuntime) requestRestart() bool {
	if _, err := reexecPath(); err != nil {
		slog.Error("restart requested, but the runwisp binary can't be found; still running", "err", err)
		return false
	}
	slog.Info("restart requested, shutting down to start again")
	rt.restart.Store(true)
	return true
}

// restartOutcome turns a clean shutdown into errRestartInPlace when a restart
// was requested, so main re-executes after the deferred cleanup has run.
func (rt *daemonRuntime) restartOutcome(runErr error) error {
	if runErr == nil && rt.restart.Load() {
		return errRestartInPlace
	}
	return runErr
}

// reexecPath is the binary a restart in place runs. It resolves os.Args[0]
// again rather than using os.Executable, so a PATH entry or symlink an upgrade
// repointed (Homebrew's Cellar, npm's global bin) picks up the new version
// instead of a file the upgrade removed.
func reexecPath() (string, error) {
	if path, err := exec.LookPath(os.Args[0]); err == nil {
		return path, nil
	}
	return os.Executable()
}

// reexec replaces this process with a fresh run of the same binary, arguments
// and environment. It only returns on failure.
func reexec() error {
	path, err := reexecPath()
	if err != nil {
		return err
	}
	return syscall.Exec(path, os.Args, os.Environ())
}
