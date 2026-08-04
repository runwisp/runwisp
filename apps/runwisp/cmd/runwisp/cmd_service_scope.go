// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"runtime"

	"github.com/runwisp/runwisp/internal/autostart"
)

// localFlagUsage is the one description of --local, shared by all five
// commands that take it so they cannot drift apart.
const localFlagUsage = "target the per-user unit under your account instead of the system-wide service"

// resolveInstallScope turns the --local flag into the systemWide boolean the
// autostart package works in, refusing the combinations that cannot work.
//
// The default is the system-wide singleton: one RunWisp per host, owned by
// root, named runwisp.service. That is what replacing crond on a box looks
// like. --local is the explicitly-requested path for a per-user daemon (or
// several of them, which is what the fingerprint suffix is for).
//
// euid and GOOS are the two things that can make the requested scope
// impossible, and each gets its own message naming the way out — a bare
// "permission denied" from systemctl three steps later is not a useful
// answer to "why didn't this install".
func resolveInstallScope(local bool, euid int) (systemWide bool, err error) {
	if local {
		// systemctl --user talks to the calling user's session bus. Under
		// sudo there is no such bus for root, so the unit would be written
		// and then never start.
		if euid == 0 && runtime.GOOS == "linux" {
			return false, &userFacingError{
				title: "--local does not work as root",
				details: "systemctl --user has no bus for root under sudo, so a user-scoped unit " +
					"installed this way would never start. Drop --local to install the system " +
					"service, or re-run without sudo as the account that should own the daemon.",
			}
		}
		return false, nil
	}

	// An OS with no installer at all falls through to the installer's own
	// ErrUnsupported (which carries the manual-setup doc link) rather than
	// being told it needs root for a scope that does not exist here.
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return false, nil
	}

	if runtime.GOOS == "darwin" {
		return false, &userFacingError{
			title: "a system-wide service is not supported on macOS yet",
			details: "RunWisp only knows how to install a per-user LaunchAgent on macOS today — " +
				"a root LaunchDaemon is not implemented. Re-run with --local to install one " +
				"under your account.",
		}
	}

	if euid != 0 {
		return false, &userFacingError{
			title: "installing the system service requires root",
			details: "Re-run as root:\n" +
				"  sudo runwisp service install\n\n" +
				"Or install a user-scoped unit under your own account instead:\n" +
				"  runwisp service install --local",
		}
	}

	return true, nil
}

// resolveManagedScope picks which installed unit the read/control commands
// (status, uninstall, stop, restart) act on. --local pins the user scope;
// otherwise we go looking for a unit rather than making the operator
// remember how the install was run — a mismatched scope used to report
// "nothing to do" while the real service stayed installed and running.
func resolveManagedScope(deps autostart.Deps, local bool) (systemWide bool, err error) {
	if local {
		return false, nil
	}
	d := autostart.DetectScope(deps)
	switch {
	case d.SystemPath == "":
		// No system scope on this OS (macOS today) — the user unit is the
		// only thing there is to look at.
		return false, nil
	case d.SystemFound && d.UserFound:
		return false, &userFacingError{
			title: "both a system-wide and a user unit are installed",
			details: fmt.Sprintf(
				"  system: %s\n  user:   %s\n\n"+
					"Pass --local to act on the user unit, or re-run without it for the system one.",
				d.SystemPath, d.UserPath),
		}
	case d.UserFound:
		return false, nil
	default:
		// Only the system unit, or nothing at all. Either way the system
		// scope is the right thing to report against: it is what
		// `service install` targets by default.
		return true, nil
	}
}
