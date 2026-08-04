// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cutover

import (
	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/config"
)

// StepKind enumerates the effects a cutover can have. The list is exhaustive, so
// a rendered plan is the whole truth about what Execute will do.
type StepKind int

const (
	// StepWriteConfig scaffolds a runwisp.toml that reads the crontabs found on
	// this box. This is the step that used to be a refusal.
	StepWriteConfig StepKind = iota + 1
	// StepWireIncludeCron surgically adds include_cron to a config that already
	// exists and declares none, leaving every other byte of it alone.
	StepWireIncludeCron
	// StepInstallService delegates wholesale to autostart: write the unit,
	// daemon-reload, stop and mask cron, enable --now, and unmask-and-restart
	// cron if RunWisp then fails to start. Its sub-steps are autostart's own,
	// rendered verbatim, so the text an operator approves is the argv that runs.
	StepInstallService
	// StepReloadRunningDaemon lifts the hold on a daemon that was already up
	// before the cutover. Absent otherwise — the daemon systemd just started read
	// its config after cron was masked, so it is holding nothing.
	StepReloadRunningDaemon
)

// Step is one line of the plan an operator approves.
type Step struct {
	Kind StepKind
	// Detail is the operator-facing summary, rendered verbatim.
	Detail string
	// Satisfied means this box is already in the desired end state for this
	// step. Rendered as done and skipped by Execute — which is what makes
	// re-running `takeover` after cron came back unmasked repair just the cron
	// half instead of reporting "already installed" and returning.
	Satisfied bool
	// Install carries autostart's plan on StepInstallService, zero otherwise.
	Install autostart.Plan
}

// BlockerKind identifies a refusal, so a caller can react to one specifically
// without matching on prose.
type BlockerKind int

const (
	BlockerNotLinux BlockerKind = iota + 1
	BlockerNeedsRoot
	BlockerNoCronJobs
	BlockerConfigUnloadable
	BlockerConfigUntrusted
	BlockerIncludeCronMissesCrontabs
	BlockerCronSourcesFailed
)

// Blocker is a reason the cutover cannot proceed. Unlike a step, nothing RunWisp
// runs will clear it.
//
// A value rather than a *userFacingError because there are three audiences:
// `takeover` renders one as a fatal error, the first run as prose it carries on
// past, and a plan listing as a bullet. The old gate returned a CLI error type,
// which is why the first-run path had to reach into its title and details and
// re-print them itself.
type Blocker struct {
	Kind BlockerKind
	// Title is one line, lower case, no trailing period.
	Title string
	// Details is the remedy, and may be several lines.
	Details string
	// Override names the flag that waives this blocker, or "" when there isn't
	// one. A blocker with no override is a genuine dead end, and the Details
	// have to carry the operator all the way out.
	Override string
}

// Evidence is everything one machine scan plus at most one config load
// established. Every blocker and every step is derived from it, so no two
// surfaces can disagree about the state of the box.
type Evidence struct {
	// Scan is the config-free machine sweep. Always populated.
	Scan config.CronScan
	// Sources renders Scan.Files as short prose ("/etc/crontab", "/etc/cron.d",
	// "deploy's crontab").
	Sources []string
	// Patterns is the include_cron list a config step would write.
	Patterns []string

	// Cfg is the loaded config; nil when none exists or it didn't load.
	Cfg *config.Config
	// ConfigExists distinguishes "no config" from "broken config".
	ConfigExists bool
	// loadErr is why the config didn't load, when ConfigExists is true and Cfg is
	// nil. Unexported: it is the raw error behind BlockerConfigUnloadable, and a
	// caller that wants it should read the blocker's Details rather than
	// re-deriving its own phrasing.
	loadErr error
	// CronIncludeDeclared is true when the raw TOML has a [daemon] include_cron
	// key at all, however non-covering. RunWisp will not rewrite one.
	CronIncludeDeclared bool
	// ReadFiles are the crontabs the config reads today (cfg.CronFiles()).
	ReadFiles []string
	// Uncovered are crontabs on this box that this config would not read.
	// Non-empty alongside CronIncludeDeclared is a blocker.
	Uncovered []string

	// CronUnit is the system cron unit a take-over would retire, and CronActive
	// whether it was running. An empty unit is not a blocker: it means there is
	// nothing to mask, not that RunWisp must refuse.
	CronUnit   string
	CronActive bool
	// UnitInstalled reports whether RunWisp's own service is already installed.
	UnitInstalled bool
	// DaemonRunning is sampled before anything is executed.
	DaemonRunning bool
}

// Plan is what a cutover would do to this box, and what stops it.
type Plan struct {
	Steps    []Step
	Blockers []Blocker
	Evidence Evidence

	// Opts is the resolved unit description, restated for the renderer.
	Opts autostart.InstallOptions
	// MasksCron is false when this host has no cron unit at all. The install is
	// still the right thing — those jobs are not held, because deriving a hold
	// needs systemctl or one of cron's pidfiles — there is just nothing to
	// retire. It is the one input Execute forwards to autostart as
	// InstallOptions.TakeOverCron, and this is the only place it is decided.
	MasksCron bool
}

// Blocked reports whether anything stops this cutover.
func (p Plan) Blocked() bool { return len(p.Blockers) > 0 }

// NothingToDo reports that the box is already in the end state — RunWisp
// installed and running, reading the crontabs, cron retired — so `takeover` can
// say so and exit 0 rather than pretending to work.
func (p Plan) NothingToDo() bool {
	if p.Blocked() {
		return false
	}
	for _, s := range p.Steps {
		if !s.Satisfied {
			return false
		}
	}
	return true
}

// FirstBlocker returns the blocker a caller should fail on, if any. Compute
// collects every blocker so a dry run doesn't make the operator fix them one at
// a time, but a command still has to exit with one reason.
func (p Plan) FirstBlocker() (Blocker, bool) {
	if len(p.Blockers) == 0 {
		return Blocker{}, false
	}
	return p.Blockers[0], true
}

// step returns the named step and whether the plan has one.
func (p Plan) step(kind StepKind) (Step, bool) {
	for i := range p.Steps {
		if p.Steps[i].Kind == kind {
			return p.Steps[i], true
		}
	}
	return Step{}, false
}

// pending reports whether the plan has the named step and it still needs doing.
func (p Plan) pending(kind StepKind) bool {
	s, ok := p.step(kind)
	return ok && !s.Satisfied
}
