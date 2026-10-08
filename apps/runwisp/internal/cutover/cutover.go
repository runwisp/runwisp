// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package cutover answers one question: what would it take for RunWisp to
// become this box's only scheduler, and is any of it impossible?
//
// The answer is a Plan computed once from the machine; `runwisp takeover`,
// `runwisp service install` and the interactive first run either render it or
// execute it. Writing the config is a step, not a prerequisite. Blockers holds
// only what no command can fix.
//
// The judgement lives here because internal/autostart never imports
// internal/config. Nothing imports this package except cmd/runwisp.
//
// Unit-install steps are not modeled here: a StepInstallService carries
// autostart's own Plan and renders its Steps verbatim, so the text an operator
// approves can never drift from the argv that runs.
package cutover

import (
	"context"
	"os/user"

	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/configedit"
)

// Deps is every seam a cutover needs. GOOS is injected and Installer is an
// interface so the decision surface is testable off Linux.
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
	// Trusted checks that an existing config is safe to bake into a root-run
	// unit. Defaults to a config.AssertFileTrusted wrapper.
	Trusted func(path string) error
	// WriteConfig scaffolds a new config at path reading patterns. Defaults to
	// writing config.CronStarterConfig; the first-run flow overrides it so a single
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
	Reload func(ctx context.Context) error

	// AllowSkippedCronJobs proceeds even though some cron sources won't load.
	// Those jobs stay stopped.
	AllowSkippedCronJobs bool
}

// Cutover binds the deps. Plan stays a pure value with no behaviour, so it can
// be rendered by a surface that cannot execute it.
type Cutover struct {
	deps Deps
}

// New fills in the production defaults for any seam the caller left nil, so a
// caller only overrides what it actually needs to fake.
func New(deps Deps) *Cutover {
	if deps.Scan == nil {
		deps.Scan = config.ScanCronSources
	}
	if deps.Trusted == nil {
		deps.Trusted = func(path string) error {
			return config.AssertFileTrusted(path, "the config file")
		}
	}
	if deps.WriteConfig == nil {
		deps.WriteConfig = func(path string, patterns []string) error {
			return configedit.WriteNew(path, config.CronStarterConfig(patterns))
		}
	}
	if deps.WireCron == nil {
		deps.WireCron = configedit.WireCronInclude
	}
	if deps.Username == "" {
		deps.Username = CurrentUsername()
	}
	return &Cutover{deps: deps}
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
