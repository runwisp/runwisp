// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package cutover owns one question: what would it take for RunWisp to become
// this box's only scheduler, and is any of it impossible?
//
// It exists because "retire cron" used to be a flag on "install a service"
// (installRequest.TakeOverCron), which meant three callers — `runwisp takeover`,
// `runwisp service install`, and the interactive first run — each re-derived
// whether a take-over was legal, in three styles, with three opinions about what
// a refusal meant. Worse, the judgement they shared could only ever *judge* a
// config: a box with cron jobs and no runwisp.toml was told to go write one,
// even though RunWisp already knew how to find the crontabs
// (config.ScanCronSources) and how to write the config that reads them
// (configedit.WriteInitWithCron).
//
// So the cutover is a value here, computed once from the machine, and every
// surface either renders it or executes it. Writing the config stops being a
// prerequisite and becomes a step. What is left in Blockers is only what no
// command can fix.
//
// This is also the only package that can hold the judgement: internal/autostart
// deliberately never imports internal/config — it is the mechanics of unit files
// and systemctl, nothing more — which is why the old gate had to be exiled up to
// cmd/runwisp. A package above both is its proper home, and cmd/runwisp becomes
// a thin adapter. Nothing imports this one except cmd/runwisp.
//
// One thing this package deliberately does NOT model: the individual steps of
// installing a unit. autostart renders those from the same systemctlInvocation
// it later executes, so the text an operator approves can never drift from the
// argv that runs. A StepInstallService carries autostart's own Plan and renders
// its Steps verbatim rather than restating them — restating them would recreate
// exactly the divergence this package exists to delete.
package cutover

import (
	"context"
	"os/user"

	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/configedit"
)

// Deps is every seam a cutover needs.
//
// Fields on a struct rather than the package-level `var scanForCron = …`,
// `cronTakeoverGOOS` and `takeoverInstall` seams they replace: those are global
// mutable state, and every test that touched one needed a t.Cleanup restore
// dance to avoid leaking into the next.
//
// Injecting GOOS, and taking Installer as an interface, is what makes the whole
// decision surface testable off Linux. The systemd mechanics in
// internal/autostart sit behind `//go:build linux`, so logic that lived next to
// them was in practice only ever exercised in a Linux CI image — which is how a
// box with no runwisp.toml came to be a dead end without anyone noticing.
type Deps struct {
	// Installer is the init-system half: unit plan, install, cron unit probe.
	Installer autostart.Installer
	// Prompter asks the operator. It already encodes --yes (auto-approve) and
	// the non-TTY refusal (ErrNeedsYes), so this package never checks either.
	Prompter autostart.Prompter

	// Opts is the resolved unit description — binary, config path, data dir,
	// host, port, scope. cmd/runwisp owns building it, since resolving it needs
	// cobra's Flag().Changed to tell an explicit --data from a default.
	Opts autostart.InstallOptions

	GOOS     string
	Euid     int
	Username string

	// Scan reports the crontabs on this machine with no config involved — the
	// unlock that lets a cutover start from nothing. Defaults to
	// config.ScanCronSources.
	Scan func(patterns []string, cfgPath string) config.CronScan
	// Load reads an existing config. Defaults to config.Load.
	Load func(path string) (*config.Config, error)
	// Trusted checks that an existing config is safe to bake into a root-run
	// unit. Defaults to a config.AssertFileTrusted wrapper.
	Trusted func(path string) error
	// WriteConfig scaffolds a new config at path reading patterns. Defaults to
	// configedit.WriteInitWithCron; the first-run flow overrides it so a single
	// "yes" can fold in an adjacent docker-compose import too.
	WriteConfig func(path string, patterns []string) error
	// WireCron inserts include_cron into a config that already exists. Defaults
	// to configedit.WireCronInclude.
	WireCron func(path string, patterns []string) error

	// Preflight is the port / data-dir conflict check. It needs a real socket,
	// so it is a seam. stale reports that our own service holds the port and the
	// settings baked into its unit are about to change.
	Preflight func(ctx context.Context) (stale bool, err error)
	// DaemonRunning reports whether a RunWisp daemon is up. Sampled once, by
	// Compute, before anything is installed: `systemctl enable --now` leaves a
	// daemon behind, so a check made afterwards would find the one systemd just
	// started and reload into a socket that is not accepting connections yet.
	DaemonRunning func() bool
	// Reload hands the held jobs to a daemon that was already running.
	Reload func() error
}

// Options are the operator's answers that aren't part of the unit description.
// --yes and --force are absent on purpose: the first lives in the Prompter, the
// second on Opts.
type Options struct {
	// AllowSkippedCronJobs proceeds even though some cron sources won't load.
	// Those jobs stay stopped.
	AllowSkippedCronJobs bool
}

// Cutover binds the deps to the operator's options. Plan stays a pure value with
// no behaviour, so it can be rendered by a surface that cannot execute it.
type Cutover struct {
	deps Deps
	opts Options
}

// New fills in the production defaults for any seam the caller left nil, so a
// caller only overrides what it actually needs to fake.
func New(deps Deps, opts Options) *Cutover {
	if deps.Scan == nil {
		deps.Scan = config.ScanCronSources
	}
	if deps.Load == nil {
		deps.Load = config.Load
	}
	if deps.Trusted == nil {
		deps.Trusted = func(path string) error {
			return config.AssertFileTrusted(path, "the config file")
		}
	}
	if deps.WriteConfig == nil {
		deps.WriteConfig = configedit.WriteInitWithCron
	}
	if deps.WireCron == nil {
		deps.WireCron = configedit.WireCronInclude
	}
	if deps.Username == "" {
		deps.Username = CurrentUsername()
	}
	return &Cutover{deps: deps, opts: opts}
}

// CurrentUsername looks up the account running this process, for
// config.DefaultCronPatterns' unprivileged branch. "" (rather than an error)
// means no per-user spool pattern can be offered — there is no name to match a
// spool file against, and guessing would scaffold a config that reads nothing.
func CurrentUsername() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.Username
}
