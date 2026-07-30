// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"os"
	"time"

	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/spf13/cobra"
)

// shouldDelegateStop reports whether `runwisp stop` should go through the
// init system instead of signalling the PID directly. Only a managed unit
// that is actually running is delegated — raw SIGTERM on a managed unit
// would leave the manager's view of the service out of sync.
func shouldDelegateStop(st autostart.Status) bool {
	return st.UnitExists && st.UnitManaged && st.Running
}

// shouldDelegateRestart reports whether `runwisp restart` should go through
// the init system. Unlike stop, a stopped-but-enabled unit also delegates:
// `systemctl restart` / `kickstart -k` starts it under the manager, whereas
// spawning our own detached process would leave two owners racing on the
// next boot.
func shouldDelegateRestart(st autostart.Status) bool {
	return st.UnitExists && st.UnitManaged && (st.Running || st.Autostart)
}

// serviceManagerName names the init system for user-facing messages.
func serviceManagerName(st autostart.Status) string {
	switch st.OS {
	case "linux":
		return "systemd"
	case "darwin":
		return "launchd"
	default:
		return "the service manager"
	}
}

// serviceState resolves the autostart installer and probes its Status.
// Any failure — unsupported OS, missing HOME, an ambiguous scope, status
// probe error — returns ok=false and the caller falls back to the direct
// PID/SIGTERM path. The service layer is best-effort sugar; it must never
// block a plain stop. local mirrors the --local flag; without it the scope
// is detected from what is actually installed.
func serviceState(cmd *cobra.Command, f Flags, local bool) (autostart.Installer, autostart.InstallOptions, autostart.Status, bool) {
	deps, err := autostart.DefaultDeps(cmd.OutOrStdout(), cmd.ErrOrStderr(), os.Stdin, true)
	if err != nil {
		return nil, autostart.InstallOptions{}, autostart.Status{}, false
	}
	systemWide, err := resolveManagedScope(deps, local)
	if err != nil {
		return nil, autostart.InstallOptions{}, autostart.Status{}, false
	}
	installer, err := autostart.New(deps)
	if err != nil {
		return nil, autostart.InstallOptions{}, autostart.Status{}, false
	}
	opts, err := resolveStatusOptions(f, systemWide)
	if err != nil {
		return nil, autostart.InstallOptions{}, autostart.Status{}, false
	}
	st, err := installer.Status(context.Background(), opts)
	if err != nil {
		return nil, autostart.InstallOptions{}, autostart.Status{}, false
	}
	return installer, opts, st, true
}

// stopWaitTimeout returns how long to wait for the daemon to exit after
// SIGTERM: the configured [daemon] shutdown_timeout plus headroom for
// process teardown, floored at 15s when the config is unreadable or the
// timeout is short.
func stopWaitTimeout(f Flags) time.Duration {
	const floor = 15 * time.Second
	cfg, err := config.Load(f.CfgFile)
	if err != nil {
		return floor
	}
	return max(cfg.Daemon.ShutdownTimeout+5*time.Second, floor)
}
