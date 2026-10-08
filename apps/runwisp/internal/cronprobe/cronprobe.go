// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package cronprobe answers one question about the machine: is a system cron
// daemon live, and in what sense.
//
// It is its own package because two layers need the same answer for different
// reasons and must never disagree. internal/config asks once per load, to decide
// which crontab-sourced tasks the scheduler has to stand down for.
// internal/runtime asks it again on a timer, so a hold releases itself when cron
// retires instead of waiting for an explicit reload — an operator who stops cron
// and forgets to reload would otherwise be left with jobs neither scheduler runs.
package cronprobe

import (
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/internal/importer"
)

// State is the answer: whether a system cron daemon is live, and prose for the
// sense in which it is ("is running", "is enabled and will start on the next
// boot", …). Prose is empty when Live is false.
type State struct {
	Live  bool
	State string
}

// PidFiles are where the common cron implementations write their pid. A package
// var so the no-systemctl fallback is testable without a real crond on the
// machine running the suite. Never assigned outside tests.
var PidFiles = []string{"/run/crond.pid", "/run/cron.pid", "/var/run/crond.pid", "/var/run/cron.pid"}

// SystemdBootedDir is the directory systemd creates when it is PID 1: the same
// check sd_booted(3) makes. A package var for the same reason PidFiles is one.
// Never assigned outside tests.
var SystemdBootedDir = "/run/systemd/system"

// ServiceProbe asks the init system whether a cron service is active or enabled.
// A package var for the same reason PidFiles is one: the real answer comes from
// systemd, and a test needs to ask the question about an init system it can
// describe. ok is false when the probe itself couldn't run (no systemctl on this
// box, or systemd isn't the running init: a non-systemd Linux, WSL or a container
// without systemd as PID 1, macOS), telling the caller to fall back to the
// pid-file check. Never assigned outside tests.
var ServiceProbe = systemctlState

// Probe reports whether a system crond looks live and, if so, in what sense.
// Prefers asking the init system directly over guessing from a pid file, since a
// stopped-but-enabled service carries the same double-fire risk with nothing
// running yet to find in a pidfile.
func Probe() State {
	if active, enabled, ok := ServiceProbe(); ok {
		switch {
		case active:
			return State{Live: true, State: "is running"}
		case enabled:
			return State{Live: true, State: "is enabled and will start on the next boot"}
		default:
			return State{}
		}
	}
	if pidRunning() {
		return State{Live: true, State: "looks like it's running (a live pidfile was found)"}
	}
	return State{}
}

// systemctlState checks importer.CronUnits() with `systemctl is-active` and
// `is-enabled`. Both matter: active covers a daemon running right now, enabled
// covers "stopped for this boot but will start on the next one", which carries
// the same double-fire risk with no process to see today.
//
// Both units and both questions go in one exec each rather than one exec per
// pair: systemctl prints one state per line in the order it was asked, and this
// runs on a timer, where six short-lived processes a minute would be wasteful.
//
// The binary existing is not enough: WSL and most container images ship
// systemctl without systemd running, and there it answers "inactive"/"offline"
// for a crond that is running right now, so the probe would release the hold and
// both schedulers would fire every job. Only trust it when systemd is PID 1.
func systemctlState() (active, enabled, ok bool) {
	if fi, err := os.Stat(SystemdBootedDir); err != nil || !fi.IsDir() {
		return false, false, false
	}
	systemctlPath, err := exec.LookPath("systemctl")
	if err != nil {
		return false, false, false
	}
	units := importer.CronUnits()
	return anyStateIs(systemctlPath, "is-active", units, "active"),
		anyStateIs(systemctlPath, "is-enabled", units, "enabled"),
		true
}

// anyStateIs reports whether any of units answers want to the given systemctl
// query.
//
// The exit code is deliberately ignored: `systemctl is-active a b c` exits 0
// only when *every* unit is active, which on a box with one cron implementation
// installed is essentially never. The stdout lines are the real answer, and
// systemctl writes them even as it exits non-zero.
func anyStateIs(systemctlPath, query string, units []string, want string) bool {
	out, _ := exec.Command(systemctlPath, append([]string{query}, units...)...).Output()
	// Fields, not lines: every state systemd prints is a single word, so splitting
	// on whitespace trims the line endings for free.
	return slices.Contains(strings.Fields(string(out)), want)
}

// pidRunning is the fallback for a box with no systemctl: read each candidate
// pid file and check that the pid it names is an actual live process, not just
// that the file exists. Checking existence alone made a crond that crashed
// without cleaning up its pid file (or a stale file left by an image build) warn
// forever about a daemon that wasn't there.
func pidRunning() bool {
	for _, path := range PidFiles {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			continue
		}
		if datadir.ProcessAlive(pid) {
			return true
		}
	}
	return false
}
