// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/runwisp/runwisp/apps/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/apps/runwisp/internal/bootid"
	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/cutover"
	"github.com/runwisp/runwisp/apps/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
	"github.com/spf13/cobra"
)

// This file is the whole adapter between cobra and internal/cutover. Every
// decision about whether RunWisp can retire cron lives in that package; this one
// resolves paths, hands over the seams it cannot reach on its own (a real socket,
// a real port), and turns its refusals into the CLI's error type.

// newCutover binds already-resolved install options to internal/cutover's seams.
//
// Deliberately cobra-free: the first-run flow builds one of these too and has no
// *cobra.Command. Where the paths come from is the caller's problem — telling an
// explicit --data from a default is Flag().Changed, which only a command knows.
//
// tweaks adjust the deps before New fills in its defaults: the first run
// replaces WriteConfig so the scaffold can also import an adjacent
// docker-compose.yml, and takeover sets AllowSkippedCronJobs.
func newCutover(f Flags, deps autostart.Deps, installer autostart.Installer, opts autostart.InstallOptions, tweaks ...func(*cutover.Deps)) *cutover.Cutover {
	// The same stable Web UI password `service install` saves. Without it the
	// daemon makes up a new one, signing everyone out, on every restart.
	opts.Password = servicePassword(installer)
	cd := cutover.Deps{
		Installer: installer,
		Prompter:  deps.Prompter,
		Opts:      opts,
		GOOS:      runtime.GOOS,
		Euid:      deps.Euid,
		Username:  deps.User,
		Preflight: func(ctx context.Context) (bool, error) {
			return preflightDaemon(ctx, installer, opts, f)
		},
		DaemonRunning: func() bool { return isDaemonRunning(f) },
		Reload:        func(ctx context.Context) error { return reloadRunningDaemon(ctx, f, deps.Stdout) },
		MarkBootRan:   func(cfgPath string) error { return markCronBootTasksRan(cfgPath, opts.DataDir) },
	}
	for _, tweak := range tweaks {
		tweak(&cd)
	}
	return cutover.New(cd)
}

// markCronBootTasksRan records in dataDir's database that the cron-sourced
// run_on_start = "boot" tasks in cfgPath already ran this boot: cron ran them
// when the machine booted, and taking over is not a reboot. The next boot has
// a new boot ID, so they run then as usual.
func markCronBootTasksRan(cfgPath, dataDir string) error {
	boot := bootid.Current()
	if boot == "" {
		return nil // no boot identity here: such tasks run on every start anyway
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	names := cronBootTasks(cfg)
	if len(names) == 0 {
		return nil
	}
	if err := datadir.EnsureDir(dataDir); err != nil {
		return err
	}
	db, err := storage.New(datadir.DBPath(dataDir))
	if err != nil {
		return err
	}
	defer db.Close()
	for _, name := range names {
		if err := db.SetTaskBootID(context.Background(), name, boot); err != nil {
			return err
		}
	}
	return nil
}

// cronBootTasks names the tasks read from a crontab's @reboot lines.
func cronBootTasks(cfg *config.Config) []string {
	var names []string
	for _, t := range cfg.Tasks {
		if t.Source == model.SourceCron && t.RunOnStart && t.RunOnStartMode == model.RunOnStartBoot {
			names = append(names, t.Name)
		}
	}
	return names
}

// resolveCutover builds the cutover for `runwisp takeover`.
//
// The scope is not negotiable here: retiring cron means masking a system unit, so
// the install is system-wide and there is no --local. The privilege and OS
// checks are Blockers in the plan, not checks here, so `takeover --dry-run` on a
// non-root macOS box still prints what it found and what would stop it.
func resolveCutover(cmd *cobra.Command, f Flags, req takeoverRequest) (*cutover.Cutover, error) {
	deps, err := autostart.DefaultDeps(cmd.OutOrStdout(), os.Stdin, req.Yes)
	if err != nil {
		return nil, err
	}

	const systemWide = true
	opts, binaryErr, err := resolveServiceOptionsTolerant(cmd, deps, f, systemWide, req.Binary, true)
	if err != nil {
		return nil, err
	}
	opts.Force = req.Force

	installer, err := autostart.New(deps)
	if err != nil {
		// An OS with no installer at all is BlockerNotLinux's job to explain, and
		// it explains it better — with the manual `import cron` route. The plan
		// never touches the installer on such a host (see Compute.probeUnits), so
		// a nil one here is the honest value rather than a landmine.
		if runtime.GOOS == "linux" {
			return nil, err
		}
		installer = nil
	}

	return newCutover(f, deps, installer, opts, func(d *cutover.Deps) {
		d.AllowSkippedCronJobs = req.AllowSkippedCronJobs
		d.BinaryProblem = binaryProblem(binaryErr)
		d.RootCommand = rootRunwisp(os.Args[1:]...)
	}), nil
}

// blockedCutoverError turns a blocked plan into the CLI's error type, so a
// take-over that cannot proceed exits non-zero and renders through the same
// pretty path as every other refusal.
//
// Every blocker is reported, not just the first: a migration where each attempt
// reveals one more reason is a guessing game.
func blockedCutoverError(p cutover.Plan) error {
	switch len(p.Blockers) {
	case 0:
		return nil
	case 1:
		b := p.Blockers[0]
		return &userFacingError{title: b.Title, details: b.Details}
	}

	var body strings.Builder
	for i, b := range p.Blockers {
		if i > 0 {
			body.WriteString("\n")
		}
		fmt.Fprintf(&body, "· %s\n", b.Title)
		if b.Details != "" {
			body.WriteString(cutover.Indent(b.Details, "  "))
		}
	}
	return &userFacingError{
		title:   fmt.Sprintf("%d things stop RunWisp taking over cron", len(p.Blockers)),
		details: body.String(),
	}
}

// asUserFacing rewraps a cutover refusal so the CLI has exactly one rendering for
// every failure it prints. Anything else passes through untouched.
func asUserFacing(err error) error {
	var ue *cutover.UserError
	if errors.As(err, &ue) {
		return &userFacingError{title: ue.Title, details: ue.Details}
	}
	return err
}

// binaryProblem unwraps the refusal text of a rejected unit binary for the plan's
// blocker, which carries its own title.
func binaryProblem(err error) error {
	if err == nil {
		return nil
	}
	if ue, ok := isUserFacing(err); ok {
		return errors.New(ue.details)
	}
	return err
}
