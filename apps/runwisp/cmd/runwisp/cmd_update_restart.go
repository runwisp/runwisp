// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/update"
	"github.com/runwisp/runwisp/internal/version"
)

// updateApplyTimeout bounds the whole self-update (resolve latest + download +
// verify + smoke test). Generous: it's an explicit operator action, not a hot path.
const updateApplyTimeout = 10 * time.Minute

// reexecRequested is set by the self-update flow so the daemon, after its normal
// SIGTERM-driven graceful shutdown, replaces its process image with the freshly
// swapped-in binary instead of exiting. Same PID keeps the pid file and any
// service-manager supervision valid; a clean exit(0) would NOT be restarted
// (both systemd and launchd units restart only on failure), so re-exec is the
// only path that works standalone and service-managed alike.
var reexecRequested atomic.Bool

func updateHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

// updatesEnabled reports whether background checking and self-update should run
// at all: opted in via [daemon] check_updates (default on) and this is a real
// release build, not a dev binary.
func updatesEnabled(cfg *daemonConfig) bool {
	return cfg.Config.Daemon.CheckUpdates && update.IsRelease(version.Version)
}

// startUpdateChecker builds and starts the background release poller, or returns
// nil when updates are disabled or this is a dev build.
func startUpdateChecker(cfg *daemonConfig) *runtime.UpdateChecker {
	if !updatesEnabled(cfg) {
		return nil
	}
	checker := runtime.NewUpdateChecker(version.Version, updateHTTPClient())
	checker.Start()
	return checker
}

// updateStatusHook exposes the poller's cached snapshot to /api/daemon, or nil
// when no checker is running.
func updateStatusHook(checker *runtime.UpdateChecker) func() (bool, string) {
	if checker == nil {
		return nil
	}
	return checker.Status
}

// selfUpdateHook returns the POST /api/daemon/update action for a writable
// standalone binary install, or nil otherwise — docker/npm/manual installs are
// upgraded through their package manager, and the UI shows that command instead.
func selfUpdateHook(cfg *daemonConfig) func() (model.UpdateResult, error) {
	if !updatesEnabled(cfg) || update.DetectMethod() != update.MethodSelf {
		return nil
	}
	return func() (model.UpdateResult, error) {
		ctx, cancel := context.WithTimeout(context.Background(), updateApplyTimeout)
		defer cancel()

		client := updateHTTPClient()
		tag, err := update.LatestRelease(ctx, client)
		if err != nil {
			return model.UpdateResult{}, fmt.Errorf("check latest release: %w", err)
		}
		if update.Compare(version.Version, tag) >= 0 {
			return model.UpdateResult{}, fmt.Errorf("already up to date (running %s, latest %s)",
				version.Version, strings.TrimPrefix(tag, "v"))
		}
		if err := update.Apply(ctx, client, tag, requestReexecRestart); err != nil {
			return model.UpdateResult{}, err
		}
		return model.UpdateResult{
			OldVersion: version.Version,
			NewVersion: strings.TrimPrefix(tag, "v"),
		}, nil
	}
}

// requestReexecRestart flags a post-shutdown re-exec and delivers SIGTERM to
// self, funnelling into the identical graceful-shutdown path a `runwisp stop`
// uses. Called by update.Apply only after a verified new binary is already on
// disk, so the shutdown that follows comes up on the new version.
func requestReexecRestart() error {
	reexecRequested.Store(true)
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		reexecRequested.Store(false)
		return fmt.Errorf("locate own process: %w", err)
	}
	slog.Info("restarting into updated binary; graceful shutdown then re-exec")
	if err := p.Signal(syscall.SIGTERM); err != nil {
		reexecRequested.Store(false)
		return fmt.Errorf("signal self for restart: %w", err)
	}
	return nil
}

// runDaemonAndReexec runs the daemon and, once it and all its defers have
// unwound cleanly, re-execs into a freshly self-updated binary if one was
// requested. Every daemon entry point routes through here.
func runDaemonAndReexec(mode daemonMode, f Flags, headless bool) error {
	if err := runDaemon(mode, f, headless); err != nil {
		return err
	}
	return maybeReexec()
}

// maybeReexec replaces the current process image with the on-disk binary when a
// self-update requested it. Called after runDaemon has fully returned and its
// defers (DB close, flock release) have unwound, so the new image starts against
// a clean data dir with the same args. syscall.Exec only returns on error.
func maybeReexec() error {
	if !reexecRequested.Load() {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable for re-exec: %w", err)
	}
	slog.Info("re-exec into updated binary", "path", exe)
	return syscall.Exec(exe, os.Args, os.Environ())
}
