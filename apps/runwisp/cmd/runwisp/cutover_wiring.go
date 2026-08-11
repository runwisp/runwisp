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

	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/cutover"
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
// tweaks adjust the seams before New fills in its defaults. One caller needs it:
// the first run replaces WriteConfig so the scaffold the plan writes can also
// import an adjacent docker-compose.yml.
func newCutover(f Flags, deps autostart.Deps, installer autostart.Installer, opts autostart.InstallOptions, opt cutover.Options, tweaks ...func(*cutover.Deps)) *cutover.Cutover {
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
		Reload:        func() error { return reloadRunningDaemon(f, deps.Stdout) },
	}
	for _, tweak := range tweaks {
		tweak(&cd)
	}
	return cutover.New(cd, opt)
}

// resolveCutover builds the cutover for `runwisp takeover`.
//
// The scope is not negotiable here: retiring cron means masking a system unit, so
// the install is system-wide and there is no --local. Neither the privilege check
// nor the OS check happens at this layer any more — both are Blockers in the plan,
// so `takeover --dry-run` on a non-root macOS box still prints what it found and
// what would stop it, instead of dying before it can say anything.
func resolveCutover(cmd *cobra.Command, f Flags, req takeoverRequest) (*cutover.Cutover, error) {
	deps, err := autostart.DefaultDeps(cmd.OutOrStdout(), os.Stdin, req.Yes)
	if err != nil {
		return nil, err
	}

	const systemWide = true
	opts, err := resolveServiceOptions(cmd, deps, f, systemWide, req.Binary)
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

	return newCutover(f, deps, installer, opts, cutover.Options{
		AllowSkippedCronJobs: req.AllowSkippedCronJobs,
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

// cutoverUserError is what internal/cutover's own operator-facing error exposes.
// It cannot return a *userFacingError directly — that type is private to package
// main — so it carries the two halves and this end rewraps them.
type cutoverUserError interface {
	Title() string
	Details() string
}

// asUserFacing rewraps a cutover refusal so the CLI has exactly one rendering for
// every failure it prints. Anything else passes through untouched.
func asUserFacing(err error) error {
	var ue cutoverUserError
	if errors.As(err, &ue) {
		return &userFacingError{title: ue.Title(), details: ue.Details()}
	}
	return err
}
