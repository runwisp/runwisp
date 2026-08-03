// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package autostart

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/runwisp/runwisp/internal/importer"
)

// ErrNoCronUnit means none of the known cron unit names are known to systemd
// on this host — Docker, sysvinit, openrc, or cron genuinely not installed.
// --take-over-cron has nothing to take over.
var ErrNoCronUnit = errors.New("autostart: no cron systemd unit found (looked for " + strings.Join(importer.CronUnits(), ", ") + ")")

// runSystemctlSystem runs a systemctl call scoped to the system manager
// regardless of whether this InstallOptions itself is --system — the cron
// unit being taken over is always a system unit. It goes through
// systemctlInvocation (the single place that decides sudo-vs-not) rather
// than hardcoding "systemctl", so cron-takeover code never re-derives that
// decision.
func (s *systemdInstaller) runSystemctlSystem(ctx context.Context, args ...string) ([]byte, []byte, error) {
	argv := systemctlInvocation(true, s.deps.Euid, args...)
	return s.deps.Cmd.Run(ctx, argv[0], argv[1:]...)
}

// probeCronUnit reads LoadState/ActiveState/UnitFileState for a candidate
// unit in one call. LoadState "not-found" (or empty, on older systemd) means
// the unit doesn't exist on this machine at all.
func (s *systemdInstaller) probeCronUnit(ctx context.Context, unit string) (loadState, activeState, unitFileState string, err error) {
	stdout, _, err := s.runSystemctlSystem(ctx, "show", "-p", "LoadState,ActiveState,UnitFileState", "--value", unit)
	if err != nil {
		return "", "", "", err
	}
	lines := strings.Split(strings.TrimRight(string(stdout), "\n"), "\n")
	for len(lines) < 3 {
		lines = append(lines, "")
	}
	return strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1]), strings.TrimSpace(lines[2]), nil
}

// discoverCronUnit finds which of the known cron unit names systemd
// actually knows about on this host, stopping at the first match so a
// caller that never asks for take-over never pays for more than zero
// probes — this must only be called when InstallOptions.TakeOverCron is
// set, or every existing ComputePlan test would see an unexpected call.
func (s *systemdInstaller) discoverCronUnit(ctx context.Context) (string, error) {
	for _, name := range importer.CronUnits() {
		loadState, _, _, err := s.probeCronUnit(ctx, name)
		if err != nil {
			continue
		}
		if loadState != "" && loadState != "not-found" {
			return name, nil
		}
	}
	return "", ErrNoCronUnit
}

// CronStatus implements Installer: it reports which cron unit this host has
// and whether it is running right now. It exists so the CLI can decide
// whether asking about a take-over is even meaningful — offering to retire
// cron on a box with no cron, or with cron already down, is noise, and
// going ahead anyway would fail later on ErrNoCronUnit.
//
// A host with no cron unit is not an error here (unlike discoverCronUnit,
// whose caller explicitly asked for a take-over); it just answers "nothing
// to take over".
func (s *systemdInstaller) CronStatus(ctx context.Context) (unit string, active bool, err error) {
	unit, err = s.discoverCronUnit(ctx)
	if err != nil {
		if errors.Is(err, ErrNoCronUnit) {
			return "", false, nil
		}
		return "", false, err
	}
	_, activeState, _, err := s.probeCronUnit(ctx, unit)
	if err != nil {
		return unit, false, err
	}
	return unit, activeState == "active", nil
}

// resolveMaskedCronUnit decides what runwisp-masked-cron marker the
// rendered unit should carry. When TakeOverCron is requested it discovers
// the live cron unit; otherwise it carries forward whatever marker the
// existing on-disk unit already records, so a plain re-install (no flag)
// never silently erases a prior take-over and leaves cron masked with
// nothing recording who did it.
func (s *systemdInstaller) resolveMaskedCronUnit(ctx context.Context, opts InstallOptions) (string, error) {
	if opts.TakeOverCron {
		return s.discoverCronUnit(ctx)
	}
	existing, err := s.deps.FS.ReadFile(s.unitPath(opts.System))
	if err != nil {
		return "", nil
	}
	parsed := extractMarkers(existing)
	if !parsed.managed {
		return "", nil
	}
	return parsed.maskedCron, nil
}

// stopAndMaskCron stops the cron unit, then masks it — in that order, since
// masking first would leave a window where a running cron could still fire
// a job on the old schedule before the stop takes effect. It reports
// whether cron was active beforehand so a failed RunWisp start can roll
// back to exactly that state.
func (s *systemdInstaller) stopAndMaskCron(ctx context.Context, unit string, out io.Writer) (wasActive bool, err error) {
	_, activeState, _, err := s.probeCronUnit(ctx, unit)
	if err != nil {
		return false, fmt.Errorf("systemctl show %s: %w", unit, err)
	}
	wasActive = activeState == "active"

	fmt.Fprintf(out, "Stopping %s\n", unit)
	if _, stderr, err := s.runSystemctlSystem(ctx, "stop", unit); err != nil {
		return wasActive, fmt.Errorf("systemctl stop %s: %w: %s", unit, err, string(stderr))
	}
	fmt.Fprintf(out, "Masking %s\n", unit)
	if _, stderr, err := s.runSystemctlSystem(ctx, "mask", unit); err != nil {
		return wasActive, fmt.Errorf("systemctl mask %s: %w: %s", unit, err, string(stderr))
	}
	return wasActive, nil
}

// unmaskCron reverses stopAndMaskCron. mask is self-inverting — it writes a
// /dev/null symlink without touching multi-user.target.wants, so unmask
// alone restores whatever enablement state cron had before — but it does
// not restart a unit that was stopped-then-masked, so restoreActive decides
// whether to also start it.
func (s *systemdInstaller) unmaskCron(ctx context.Context, unit string, restoreActive bool, out io.Writer) error {
	fmt.Fprintf(out, "Unmasking %s\n", unit)
	if _, stderr, err := s.runSystemctlSystem(ctx, "unmask", unit); err != nil {
		return fmt.Errorf("systemctl unmask %s: %w: %s", unit, err, string(stderr))
	}
	if !restoreActive {
		return nil
	}
	fmt.Fprintf(out, "Starting %s\n", unit)
	if _, stderr, err := s.runSystemctlSystem(ctx, "start", unit); err != nil {
		return fmt.Errorf("systemctl start %s: %w: %s", unit, err, string(stderr))
	}
	return nil
}

// cronMarkerFromUnitFile reads the runwisp-masked-cron marker (if any) from
// the unit already on disk at unitPath, for uninstall — which must only
// ever unmask a unit it can prove it masked itself.
func (s *systemdInstaller) cronMarkerFromUnitFile(unitPath string) string {
	existing, err := s.deps.FS.ReadFile(unitPath)
	if err != nil {
		return ""
	}
	return extractMarkers(existing).maskedCron
}
