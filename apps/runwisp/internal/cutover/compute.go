// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cutover

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/configedit"
	"github.com/runwisp/runwisp/internal/textutil"
)

// Compute answers "what would a cutover do to this box, and what stops it",
// without touching anything.
//
// It returns an error only for a probe that genuinely failed. Every
// operator-facing "no" is a Blocker inside the value — that is the property that
// makes --dry-run total, and it is the fix for a dry run that used to exit with
// the cron gate's error instead of printing a plan.
//
// Blockers accumulate rather than short-circuit: reporting one at a time turns a
// migration into a guessing game.
func (c *Cutover) Compute(ctx context.Context) (Plan, error) {
	ev, err := c.gatherEvidence(ctx)
	if err != nil {
		return Plan{}, err
	}

	p := Plan{
		Evidence:  ev,
		Opts:      c.deps.Opts,
		MasksCron: ev.CronUnit != "",
		Blockers:  c.blockers(ev),
	}
	// The unit plan is what autostart would do, and its steps are the only
	// description of them anyone should render. Skipped on a blocked plan: it
	// costs systemctl calls, and a plan nobody can run has no use for it.
	if !p.Blocked() {
		steps, err := c.installStep(ctx, p)
		if err != nil {
			return Plan{}, err
		}
		p.Steps = steps
	} else {
		p.Steps = c.configSteps(ev)
	}
	return p, nil
}

// gatherEvidence takes one look at the machine: the crontabs on it, the config
// (if any), and the two units that matter. Nothing here decides anything.
func (c *Cutover) gatherEvidence(ctx context.Context) (Evidence, error) {
	ev := Evidence{Patterns: config.DefaultCronPatterns(c.deps.Euid, c.deps.Username)}

	cfgPath := c.deps.Opts.Config
	ev.Scan = c.deps.Scan(ev.Patterns, cfgPath)
	ev.Sources = DescribeSources(ev.Scan.Files)
	// Prefer the globs the scan actually resolved, so a scaffolded config names
	// patterns that mean something on this box.
	if len(ev.Scan.Globs) > 0 {
		ev.Patterns = ev.Scan.Globs
	}

	if err := c.readConfig(&ev, cfgPath); err != nil {
		return Evidence{}, err
	}

	if err := c.probeUnits(ctx, &ev); err != nil {
		return Evidence{}, err
	}
	ev.DaemonRunning = c.deps.DaemonRunning != nil && c.deps.DaemonRunning()
	return ev, nil
}

// readConfig fills in the config half of the evidence. A config that doesn't
// load is recorded (ConfigExists true, Cfg nil) rather than returned as an
// error: it is a blocker, and the plan should still be able to say what it found
// on the box around it.
//
// The order matters. Load runs before anything decides to wire include_cron in,
// because a surgical edit to a file whose [daemon] header we cannot trust is how
// you turn a broken config into a differently broken one.
func (c *Cutover) readConfig(ev *Evidence, path string) error {
	raw, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		// Unreadable-but-present (a permission problem, a directory) is evidence,
		// not a crash: the blocker below names it.
		ev.ConfigExists = true
		ev.loadErr = err
		return nil
	}
	ev.ConfigExists = true

	// Whether the key is declared at all is EnsureCronInclude's dry refusal —
	// one implementation of that probe, in the package that owns the edit.
	if _, err := configedit.EnsureCronInclude(raw, ev.Patterns); errors.Is(err, configedit.ErrCronIncludeAlreadySet) {
		ev.CronIncludeDeclared = true
	}

	cfg, err := c.deps.Load(path)
	if err != nil {
		ev.loadErr = err
		return nil
	}
	ev.Cfg = cfg
	// "Does this config read crontabs" is answered the only way that can't
	// drift: ask the loader.
	ev.ReadFiles = cfg.CronFiles()
	for _, f := range ev.Scan.Files {
		if !slices.Contains(ev.ReadFiles, f) {
			ev.Uncovered = append(ev.Uncovered, f)
		}
	}
	return nil
}

// probeUnits asks the init system about cron and about RunWisp's own service.
// Skipped entirely on a host that can't do a take-over: there is no systemd to
// ask, and a plan already carrying BlockerNotLinux has no use for the answer.
func (c *Cutover) probeUnits(ctx context.Context, ev *Evidence) error {
	if c.deps.GOOS != "linux" {
		return nil
	}
	unit, active, err := c.deps.Installer.CronStatus(ctx)
	if err != nil {
		return fmt.Errorf("probe the system cron unit: %w", err)
	}
	ev.CronUnit, ev.CronActive = unit, active

	st, err := c.deps.Installer.Status(ctx, c.deps.Opts)
	if err != nil {
		return fmt.Errorf("probe the runwisp service: %w", err)
	}
	ev.UnitInstalled = st.Installed
	return nil
}

// blockers is every reason this cutover cannot proceed, in the order an operator
// would want them explained: the machine first (cheapest and most absolute),
// then the config, then its contents.
func (c *Cutover) blockers(ev Evidence) []Blocker {
	var out []Blocker

	switch {
	case c.deps.GOOS != "linux":
		// Nothing below can matter on a host where masking cron is impossible.
		return []Blocker{unsupportedOSBlocker(c.deps.GOOS)}
	case c.deps.Euid != 0:
		out = append(out, Blocker{
			Kind:  BlockerNeedsRoot,
			Title: "taking over cron requires root and the system service",
			Details: "A user-scoped daemon cannot execute another account's cron jobs, and " +
				"`systemctl --user` has no bus for root under sudo.\n\n  sudo runwisp takeover",
		})
	}

	if ev.Scan.Jobs == 0 && len(ev.ReadFiles) == 0 {
		out = append(out, noCronJobsBlocker(ev))
	}
	out = append(out, c.configBlockers(ev)...)
	out = append(out, c.cronSourceBlockers(ev)...)
	return out
}

// unsupportedOSBlocker is the answer on a host where RunWisp cannot retire cron
// at all. There is nothing to fix, so it names the manual route rather than
// leaving the operator to find it.
func unsupportedOSBlocker(goos string) Blocker {
	details := fmt.Sprintf(
		"RunWisp retires cron by stopping and masking its systemd unit, and this host runs %s.\n", goos)
	if goos == "darwin" {
		details += "macOS's cron lives under SIP-protected /System/Library/LaunchDaemons, so nothing " +
			"running as you can mask it — and RunWisp has no system-wide install there to mask it from.\n"
	}
	return Blocker{
		Kind:  BlockerNotLinux,
		Title: "taking over cron needs Linux with systemd",
		Details: details + "\nConvert the jobs and retire the crontab by hand instead:\n" +
			"  1. runwisp import cron --write   (the jobs become RunWisp tasks)\n" +
			"  2. crontab -r                    (once you've seen them run under RunWisp)\n\n" +
			"Docs: https://docs.runwisp.com/coming-from/crontabs/",
	}
}

// noCronJobsBlocker is all that survives of the old "no cron jobs are being
// read", and it now means something completely different: not "your config isn't
// set up" — RunWisp fixes that itself — but "there is nothing on this box to
// take over".
func noCronJobsBlocker(ev Evidence) Blocker {
	where := "the usual cron paths"
	if len(ev.Patterns) > 0 {
		where = strings.Join(ev.Patterns, ", ")
	}
	return Blocker{
		Kind:  BlockerNoCronJobs,
		Title: "no cron jobs on this box",
		Details: fmt.Sprintf("RunWisp looked in %s and found nothing that defines a job.\n\n", where) +
			"There is nothing to take over, and masking cron would just remove a scheduler " +
			"with no work — including any job RunWisp cannot see.\n\n" +
			"If your jobs live somewhere else, point [daemon] include_cron at them and re-run:\n" +
			"https://docs.runwisp.com/configuration/daemon/#include_cron",
	}
}

// configBlockers covers the two ways an existing config stops a cutover: it
// won't load, or it isn't safe to bake into a root-run unit. A missing config is
// not among them — that is StepWriteConfig's job now.
func (c *Cutover) configBlockers(ev Evidence) []Blocker {
	if !ev.ConfigExists {
		return nil
	}
	path := c.deps.Opts.Config

	if ev.loadErr != nil {
		return []Blocker{{
			Kind:  BlockerConfigUnloadable,
			Title: fmt.Sprintf("%s does not load", path),
			Details: ev.loadErr.Error() +
				"\n\nNothing was written, and cron is untouched. RunWisp will not mask cron while it " +
				"cannot read its own config — that would leave this box with no scheduler at all.",
		}}
	}

	var out []Blocker
	// Checked here rather than during path resolution so a --dry-run reports it
	// alongside everything else instead of dying before it can print a plan.
	if c.deps.Opts.System {
		if err := c.deps.Trusted(path); err != nil {
			out = append(out, Blocker{
				Kind:  BlockerConfigUntrusted,
				Title: fmt.Sprintf("%s is not trusted for a system-wide install", path),
				Details: err.Error() +
					"\n\nThe system service runs as root and would execute whatever this file says. " +
					"Lock it down first:\n" +
					fmt.Sprintf("  sudo chown root:root %s && sudo chmod go-w %s", path, path),
			})
		}
	}
	if ev.CronIncludeDeclared && len(ev.Uncovered) > 0 {
		out = append(out, includeCronMismatchBlocker(path, ev))
	}
	return out
}

// includeCronMismatchBlocker fires when the operator already maintains an
// include_cron list and it isn't picking up every crontab on this box.
//
// Deliberately not overridable. Masking cron while /etc/cron.d/backup goes
// unread silently stops that job, which is the one failure mode RunWisp exists
// to prevent — and the remedy is a paste the blocker prints in full.
func includeCronMismatchBlocker(path string, ev Evidence) Blocker {
	return Blocker{
		Kind: BlockerIncludeCronMissesCrontabs,
		Title: fmt.Sprintf("%s sets its own include_cron, and %s on this box %s not in it",
			path, textutil.Count(len(ev.Uncovered), "crontab", "crontabs"), textutil.Pluralize(len(ev.Uncovered), "is", "are")),
		Details: "Not read by this config:\n" +
			Indent(strings.Join(ev.Uncovered, "\n"), "  ") +
			"\nMasking cron would stop those jobs with nothing left scheduling them. RunWisp will " +
			"not edit an include_cron you wrote — add them to it and re-run:\n\n" +
			Indent(config.CronIncludeArray(ev.Patterns), "  "),
	}
}

// cronSourceBlockers reports the cron sources that won't load: the ones refused
// outright (an untrusted file, an owner this daemon can't become) and the ones
// skipped job by job. One blocker with one override, because it is one question
// to the operator — "some of your jobs won't run under RunWisp; hand cron over
// anyway?"
//
// Both halves come from the pre-config scan, which sees them whether or not a
// runwisp.toml exists yet. Reading the per-job skips only off a loaded config was
// why the first `takeover` on a bare box asked for nothing and the second one —
// same box, nothing changed, and a re-run the command promises is safe — hard
// blocked demanding --allow-skipped-cron-jobs.
//
// A loaded config is still consulted, for the skips in a crontab the operator's
// own include_cron reaches and the machine sweep does not. Deduplicated by the
// rendered reason: scan and config render the same finding identically, so the
// same skip counted twice would make the gate disagree with itself again.
func (c *Cutover) cronSourceBlockers(ev Evidence) []Blocker {
	var out []Blocker

	reasons := slices.Clone(ev.Scan.Blocked)
	reasons = append(reasons, ev.Scan.Skipped...)
	if ev.Cfg != nil {
		for _, f := range ev.Cfg.CronFindings {
			if f.Skipped && !slices.Contains(reasons, f.String()) {
				reasons = append(reasons, f.String())
			}
		}
	}
	if len(reasons) > 0 && !c.opts.AllowSkippedCronJobs {
		out = append(out, Blocker{
			Kind:  BlockerCronSourcesFailed,
			Title: fmt.Sprintf("%d cron source(s) failed to load", len(reasons)),
			Details: strings.Join(reasons, "\n") +
				"\n\nFix these first, or pass --allow-skipped-cron-jobs to take over cron anyway " +
				"(those jobs stay stopped).",
		})
	}

	return out
}

// configSteps is the config half of the plan: exactly one of write, wire, or
// already-done. They are mutually exclusive — configedit refuses to clobber an
// existing file — so Execute picks its action from the step kind, never from a
// second look at the disk.
func (c *Cutover) configSteps(ev Evidence) []Step {
	sources := "your crontabs"
	if len(ev.Sources) > 0 {
		sources = strings.Join(ev.Sources, ", ")
	}

	switch {
	case !ev.ConfigExists:
		return []Step{{Kind: StepWriteConfig, Detail: fmt.Sprintf(
			"Write %s\n       reads %s live via [daemon] include_cron — nothing is converted or\n"+
				"       rewritten, and crontab -e keeps working",
			c.deps.Opts.Config, sources)}}
	case len(ev.ReadFiles) > 0:
		return []Step{{Kind: StepWireIncludeCron, Satisfied: true, Detail: fmt.Sprintf(
			"%s already reads %s", c.deps.Opts.Config, sources)}}
	default:
		return []Step{{Kind: StepWireIncludeCron, Detail: fmt.Sprintf(
			"Add include_cron to %s\n       so it reads %s — your comments, key order and\n"+
				"       formatting are left untouched",
			c.deps.Opts.Config, sources)}}
	}
}

// installStep appends the unit half of the plan to the config half.
//
// The install is one step carrying autostart's own Plan rather than a restatement
// of it. A PlanNoop means the unit on disk already matches, which is satisfied
// only if cron is also already retired: a marker-carrying unit whose cron came
// back is not "done", it is the reassert case, and treating it as satisfied is
// how re-running `takeover` used to print "Already installed ✓" and return on a
// box that was double-firing.
func (c *Cutover) installStep(ctx context.Context, p Plan) ([]Step, error) {
	opts := p.Opts
	opts.TakeOverCron = p.MasksCron

	unitPlan, err := c.deps.Installer.ComputePlan(ctx, opts)
	if err != nil {
		return nil, err
	}

	detail := "Install RunWisp as a system service, so it starts on boot"
	if p.MasksCron {
		detail = fmt.Sprintf("Install RunWisp as a system service and retire %s", p.Evidence.CronUnit)
	}
	satisfied := unitPlan.Kind == autostart.PlanNoop && !p.Evidence.CronActive && p.Evidence.UnitInstalled

	steps := append(c.configSteps(p.Evidence), Step{
		Kind:      StepInstallService,
		Detail:    detail,
		Satisfied: satisfied,
		Install:   unitPlan,
	})

	if p.Evidence.DaemonRunning {
		steps = append(steps, Step{
			Kind:   StepReloadRunningDaemon,
			Detail: "Reload the running daemon, so the jobs it was holding become its own",
		})
	}
	return steps, nil
}

// Indent prefixes every non-empty line of s with pad, and guarantees a trailing
// newline so callers can concatenate blocks.
func Indent(s, pad string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, ln := range lines {
		if ln != "" {
			lines[i] = pad + ln
		}
	}
	return strings.Join(lines, "\n") + "\n"
}
