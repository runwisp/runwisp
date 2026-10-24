// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build linux

package autostart

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/runwisp/runwisp/apps/runwisp/internal/importer"
)

// ErrNoCronUnit means none of the known cron unit names are known to systemd
// on this host — Docker, sysvinit, openrc, or cron genuinely not installed.
// a take-over has nothing to take over.
var ErrNoCronUnit = errors.New("autostart: no cron systemd unit found (looked for " + strings.Join(importer.CronUnits(), ", ") + ")")

// probeCronUnit reads LoadState/ActiveState/UnitFileState for a candidate
// unit in one call. LoadState "not-found" (or empty, on older systemd) means
// the unit doesn't exist on this machine at all.
func (s *systemdInstaller) probeCronUnit(ctx context.Context, unit string) (loadState, activeState, unitFileState string, err error) {
	stdout, _, err := s.querySystemctl(ctx, true, "show", "-p", "LoadState,ActiveState,UnitFileState", "--value", unit)
	if err != nil {
		return "", "", "", err
	}
	lines := strings.Split(strings.TrimRight(string(stdout), "\n"), "\n")
	for len(lines) < 3 {
		lines = append(lines, "")
	}
	return strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1]), strings.TrimSpace(lines[2]), nil
}

// discoverCronUnits finds the units a take-over retires: the first of the known
// cron unit names systemd actually knows about on this host, plus anacron's
// units when it has them (anacron, not cron, runs cron.daily/weekly/monthly on
// Debian, Ubuntu and Fedora, and RunWisp now reads /etc/anacrontab itself).
//
// units is the space-separated list the runwisp-masked-cron marker records, and
// priors the matching runwisp-cron-prior-state values, paired by position;
// systemctl takes the same list in one call. active reports whether the cron
// unit itself is running.
//
// This must only be called when InstallOptions.TakeOverCron is set (or by
// CronStatus), so an install that never asks for a take-over pays for no
// probes at all.
func (s *systemdInstaller) discoverCronUnits(ctx context.Context) (units, priors string, active bool, err error) {
	var names, states []string
	probe := func(candidates []string, firstOnly bool) {
		for _, name := range candidates {
			loadState, activeState, unitFileState, err := s.probeCronUnit(ctx, name)
			if err != nil || loadState == "" || loadState == "not-found" {
				continue
			}
			names = append(names, name)
			states = append(states, cronPriorState(activeState, unitFileState))
			if firstOnly {
				active = activeState == "active"
				return
			}
		}
	}
	probe(importer.CronUnits(), true)
	if len(names) == 0 {
		return "", "", false, ErrNoCronUnit
	}
	probe(importer.AnacronUnits(), false)
	return strings.Join(names, " "), strings.Join(states, " "), active, nil
}

// CronStatus implements Installer: it reports which cron units this host has
// (see discoverCronUnits) and whether cron is running right now. It exists so
// the CLI can decide whether asking about a take-over is even meaningful —
// offering to retire cron on a box with no cron, or with cron already down, is
// noise, and going ahead anyway would fail later on ErrNoCronUnit.
//
// A host with no cron unit is not an error here (unlike discoverCronUnits,
// whose caller explicitly asked for a take-over); it just answers "nothing
// to take over".
func (s *systemdInstaller) CronStatus(ctx context.Context) (unit string, active bool, err error) {
	units, _, active, err := s.discoverCronUnits(ctx)
	if errors.Is(err, ErrNoCronUnit) {
		return "", false, nil
	}
	return units, active, err
}

// The runwisp-cron-prior-state values: what the cron unit looked like before
// RunWisp first touched it, so every undo (uninstall, rollback) restores
// exactly that instead of assuming cron was running.
const (
	cronPriorActive   = "active"
	cronPriorInactive = "inactive"
	// cronPriorMasked means the operator had masked cron themselves. RunWisp
	// never owned that mask, so nothing it does may lift it.
	cronPriorMasked = "masked"
)

// cronPriorState classifies a probe into a runwisp-cron-prior-state value.
// Enablement needs no slot: mask never touches it, so unmask alone restores it.
func cronPriorState(activeState, unitFileState string) string {
	switch {
	case strings.HasPrefix(unitFileState, "masked"): // "masked" or "masked-runtime"
		return cronPriorMasked
	case activeState == "active":
		return cronPriorActive
	default:
		return cronPriorInactive
	}
}

// resolveMaskedCronUnit decides what runwisp-masked-cron and
// runwisp-cron-prior-state markers the rendered unit should carry. When
// TakeOverCron is requested it discovers the live cron unit; otherwise it
// carries forward whatever the existing on-disk unit already records, so a
// plain re-install (no flag) never silently erases a prior take-over and
// leaves cron masked with nothing recording who did it.
func (s *systemdInstaller) resolveMaskedCronUnit(ctx context.Context, opts InstallOptions) (unit, prior string, err error) {
	var recorded parsedUnit
	if existing, err := s.deps.FS.ReadFile(s.unitPath(opts.System)); err == nil {
		if parsed := extractMarkers(existing); parsed.managed {
			recorded = parsed
		}
	}
	if !opts.TakeOverCron {
		return recorded.maskedCron, recorded.cronPrior, nil
	}
	unit, prior, _, err = s.discoverCronUnits(ctx)
	if err != nil {
		return "", "", err
	}
	// A re-run finds cron masked because RunWisp masked it. Recording that as
	// the prior state would make uninstall leave cron masked forever, so the
	// state recorded by the first take-over stands for every unit it recorded.
	return unit, keepRecordedPriors(unit, prior, recorded.maskedCron, recorded.cronPrior), nil
}

// keepRecordedPriors replaces each unit's freshly probed prior state with the
// one an earlier take-over recorded for it. A recorded unit with no recorded
// state (a unit file that predates the prior-state marker) counts as active,
// which is what restoreCron assumed of it before.
func keepRecordedPriors(units, priors, recordedUnits, recordedPriors string) string {
	if units == recordedUnits {
		return recordedPriors // verbatim, so an unchanged take-over re-renders byte for byte
	}
	recorded := map[string]string{}
	recPriors := strings.Fields(recordedPriors)
	for i, u := range strings.Fields(recordedUnits) {
		recorded[u] = cronPriorActive
		if i < len(recPriors) {
			recorded[u] = recPriors[i]
		}
	}
	out := strings.Fields(priors)
	for i, u := range strings.Fields(units) {
		if p, ok := recorded[u]; ok && i < len(out) {
			out[i] = p
		}
	}
	return strings.Join(out, " ")
}

// stopAndMaskCron stops the cron units, then masks them — in that order, since
// masking first would leave a window where a running cron could still fire
// a job on the old schedule before the stop takes effect. It reports the
// state each unit was in beforehand (cronPrior* values, paired with units by
// position) so a failed RunWisp start can roll back to exactly that state, and
// separately whether `systemctl stop` itself succeeded: a probe or stop failure
// means cron was never actually stopped, so the caller must not roll back a
// take-over that never took — unmasking a unit that was never masked, or
// starting one that was never stopped, would just poke cron for no reason.
func (s *systemdInstaller) stopAndMaskCron(ctx context.Context, units string, out io.Writer) (priors string, stopped bool, err error) {
	names := strings.Fields(units)
	states := make([]string, 0, len(names))
	for _, unit := range names {
		_, activeState, unitFileState, err := s.probeCronUnit(ctx, unit)
		if err != nil {
			return "", false, fmt.Errorf("systemctl show %s: %w", unit, err)
		}
		states = append(states, cronPriorState(activeState, unitFileState))
	}
	priors = strings.Join(states, " ")

	fmt.Fprintf(out, "Stopping %s\n", units)
	// Cron is always a system unit, whichever manager RunWisp runs under.
	if _, stderr, err := s.runSystemctl(ctx, true, append([]string{"stop"}, names...)...); err != nil {
		return priors, false, fmt.Errorf("systemctl stop %s: %w: %s", units, err, string(stderr))
	}
	stopped = true

	fmt.Fprintf(out, "Masking %s\n", units)
	if _, stderr, err := s.runSystemctl(ctx, true, append([]string{"mask"}, names...)...); err != nil {
		return priors, stopped, fmt.Errorf("systemctl mask %s: %w: %s", units, err, string(stderr))
	}
	return priors, stopped, nil
}

// restoreCron reverses stopAndMaskCron back to priors. mask is self-inverting
// — it writes a /dev/null symlink without touching multi-user.target.wants,
// so unmask alone restores whatever enablement state a unit had before — but it
// does not restart a unit that was stopped-then-masked, so each unit's prior
// decides whether to also start it. A unit with no recorded prior (a unit file
// that predates the marker) is unmasked and started.
func (s *systemdInstaller) restoreCron(ctx context.Context, units, priors string, out io.Writer) error {
	states := strings.Fields(priors)
	var unmask, start []string
	for i, unit := range strings.Fields(units) {
		prior := ""
		if i < len(states) {
			prior = states[i]
		}
		switch prior {
		case cronPriorMasked:
			fmt.Fprintf(out, "Leaving %s masked, as it was before the take-over\n", unit)
			continue
		case cronPriorInactive:
		default:
			start = append(start, unit)
		}
		unmask = append(unmask, unit)
	}
	if len(unmask) == 0 {
		return nil
	}
	fmt.Fprintf(out, "Unmasking %s\n", strings.Join(unmask, " "))
	if _, stderr, err := s.runSystemctl(ctx, true, append([]string{"unmask"}, unmask...)...); err != nil {
		return fmt.Errorf("systemctl unmask %s: %w: %s", strings.Join(unmask, " "), err, string(stderr))
	}
	if len(start) == 0 {
		return nil
	}
	fmt.Fprintf(out, "Starting %s\n", strings.Join(start, " "))
	if _, stderr, err := s.runSystemctl(ctx, true, append([]string{"start"}, start...)...); err != nil {
		return fmt.Errorf("systemctl start %s: %w: %s", strings.Join(start, " "), err, string(stderr))
	}
	return nil
}

// restartsAny reports whether restoreCron would start any unit back up: one that
// was running before, or one with no recorded state.
func restartsAny(priors string) bool {
	if priors == "" {
		return true
	}
	for _, p := range strings.Fields(priors) {
		if p != cronPriorInactive && p != cronPriorMasked {
			return true
		}
	}
	return false
}

// handBackUnits is the part of a take-over RunWisp may undo: the units it
// masked, minus any the operator had already masked themselves.
func handBackUnits(units, priors string) string {
	states := strings.Fields(priors)
	var out []string
	for i, unit := range strings.Fields(units) {
		if i < len(states) && states[i] == cronPriorMasked {
			continue
		}
		out = append(out, unit)
	}
	return strings.Join(out, " ")
}

// reassertCronTakeover re-runs the take-over on an install that had nothing to
// write. A unit whose marker already records the take-over renders byte-for-byte
// like the one a fresh take-over would produce, so ComputePlan calls it a
// no-op — but cron can have come back since (an operator unmasked it, a package
// upgrade brought it back), and reporting "already installed ✓" on a box with
// two schedulers is exactly the silent double-fire the take-over exists to end.
//
// Cheap when there is nothing wrong: one probe and an `enable --now` that is a
// no-op on a running unit, no output, no writes.
//
// It does not ask. Consent for stopping and masking cron is the caller's, taken
// once against a plan that spells the mask out: either internal/cutover's (which
// sets PreConfirmed) or this package's own banner and "Proceed?" for anyone else.
// Asking a second question here for the same decision is how an operator learns
// to stop reading prompts.
func (s *systemdInstaller) reassertCronTakeover(ctx context.Context, opts InstallOptions, plan Plan, out io.Writer) error {
	if !opts.TakeOverCron || plan.CronUnit == "" {
		return nil
	}
	retired := true
	for _, unit := range strings.Fields(plan.CronUnit) {
		_, activeState, unitFileState, err := s.probeCronUnit(ctx, unit)
		if err != nil {
			return fmt.Errorf("systemctl show %s: %w", unit, err)
		}
		retired = retired && activeState != "active" && unitFileState == "masked"
	}
	if retired {
		// Cron is retired, so RunWisp is the only scheduler left. The unit may
		// still be stopped (`runwisp stop`), which leaves nothing running the
		// jobs; `enable --now` brings it back and is a no-op when it is up.
		return s.runEnableNow(ctx, opts.System)
	}
	fmt.Fprintf(out, "%s is back since the take-over — it and RunWisp are both running your jobs.\n", plan.CronUnit)
	cronPrior, stopped, err := s.stopAndMaskCron(ctx, plan.CronUnit, out)
	if err != nil {
		if stopped {
			s.rollbackCronTakeover(ctx, opts, plan, cronPrior, out)
		}
		return fmt.Errorf("take over cron: %w", err)
	}
	// The unit was already on disk, but nothing so far proves the service is
	// actually up, and masking cron while RunWisp is down would leave the box
	// with no scheduler at all. Same order and same rollback as a real install;
	// `enable --now` is a no-op when the unit is already running.
	if err := s.runEnableNow(ctx, opts.System); err != nil {
		s.rollbackCronTakeover(ctx, opts, plan, cronPrior, out)
		return err
	}
	return nil
}

// cronMarkerFromUnitFile reads the runwisp-masked-cron and
// runwisp-cron-prior-state markers (if any) from the unit already on disk at
// unitPath, for uninstall — which must only ever unmask a unit it can prove it
// masked itself, and only back to the state it found it in.
func (s *systemdInstaller) cronMarkerFromUnitFile(unitPath string) (unit, prior string) {
	existing, err := s.deps.FS.ReadFile(unitPath)
	if err != nil {
		return "", ""
	}
	parsed := extractMarkers(existing)
	return parsed.maskedCron, parsed.cronPrior
}

// cronFailsafeUnitName is the oneshot a take-over installs next to the
// system-wide runwisp.service; its OnFailure= starts it once systemd gives up
// restarting RunWisp, so cron runs the jobs again instead of nothing.
const cronFailsafeUnitName = "runwisp-cron-failsafe.service"

func cronFailsafePath() string {
	return filepath.Join(systemdSystemUnitDir, cronFailsafeUnitName)
}

// wantsCronFailsafe reports whether the unit rendered from opts gets the
// failsafe. Only a system-wide unit can: a user unit's OnFailure= cannot reach
// the system manager that owns cron. And only when there is a cron to hand
// back to, not one the operator had masked themselves.
func wantsCronFailsafe(opts InstallOptions) bool {
	return opts.System && handBackUnits(opts.maskedCronUnit, opts.cronPriorState) != ""
}
