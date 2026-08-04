// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cutover

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/configedit"
)

// Result is what Execute actually did, so a caller can report accurately instead
// of restating the plan it hoped for.
type Result struct {
	// ConfigWritten is the path a scaffold created, or "" if none was.
	ConfigWritten string
	// IncludeWired reports that an existing config gained include_cron.
	IncludeWired bool
	// ServiceInstalled reports that the unit was written and started.
	ServiceInstalled bool
	// Reloaded reports that an already-running daemon was reconciled.
	Reloaded bool
	// Aborted reports that the operator declined at the installer's own prompt.
	// Not an error: nothing was masked, so the box is exactly as it was.
	Aborted bool
	// SettingsStale reports that our own live service holds the port and the
	// settings baked into its unit changed — systemd keeps a running unit on the
	// settings it started with, so "installed and started" would otherwise mean
	// "…and still serving the old port".
	SettingsStale bool
}

// Confirm asks the one question a cutover asks, after the caller has rendered the
// plan it refers to.
//
// It lives here rather than in the calling command so the question is phrased in
// the same place the plan is: PromptQuestion names the unit being retired, and a
// caller that wrote its own prompt could ask about a unit the plan does not
// touch. The Prompter already encodes --yes (auto-approve) and the non-TTY
// refusal, so an unattended `takeover --yes` needs nothing extra here and an
// unattended run without it fails rather than hanging.
//
// The default is yes: the operator typed a command whose entire purpose is this.
func (c *Cutover) Confirm(p Plan) (bool, error) {
	return c.deps.Prompter.Confirm(PromptQuestion(p), true)
}

// Execute performs the plan's unsatisfied steps, in order.
//
// The config comes first, unconditionally, because the installer's own preflight
// refuses a unit pointing at a config that doesn't exist — and because a cutover
// that masked cron before RunWisp could read the crontabs would be the exact
// failure this whole path exists to avoid.
//
// It deliberately does not re-run Compute. A plan is a value the operator
// approved; re-deriving it here would mean the thing that ran could differ from
// the thing that was agreed to.
func (c *Cutover) Execute(ctx context.Context, p Plan, out io.Writer) (Result, error) {
	var res Result
	if p.Blocked() {
		// Callers are expected to check, but executing a blocked plan would mask
		// cron on a box RunWisp cannot schedule for — worth being unable to do.
		b, _ := p.FirstBlocker()
		return res, fmt.Errorf("cannot take over cron: %s", b.Title)
	}

	if err := c.applyConfig(p, &res, out); err != nil {
		return res, err
	}

	if p.pending(StepInstallService) {
		installed, stale, err := c.install(ctx, p, out)
		if err != nil {
			return res, err
		}
		res.ServiceInstalled, res.SettingsStale = installed, stale
		if !installed {
			// Declined at the installer's prompt. Nothing was masked, so there is
			// no daemon state to reconcile either.
			res.Aborted = true
			return res, nil
		}
	}

	if p.pending(StepReloadRunningDaemon) {
		fmt.Fprintln(out, "Reloading the running daemon so the held jobs become RunWisp's...")
		if err := c.deps.Reload(); err != nil {
			return res, err
		}
		res.Reloaded = true
	}
	return res, nil
}

// applyConfig runs whichever of the two config steps the plan carries.
//
// The os.Stat re-check is not redundant with Compute. An operator can sit at the
// confirmation prompt for as long as they like, and configedit refuses to clobber
// an existing file — so a config that appeared in between has to surface as an
// error, never as a silent overwrite of something RunWisp didn't write.
func (c *Cutover) applyConfig(p Plan, res *Result, out io.Writer) error {
	path := p.Opts.Config
	patterns := p.Evidence.Patterns

	switch {
	case p.pending(StepWriteConfig):
		if _, err := os.Stat(path); err == nil {
			return &userError{
				title: fmt.Sprintf("%s appeared while RunWisp was asking", path),
				details: "The plan was built when there was no config there, so RunWisp will not " +
					"overwrite it. Nothing was written and cron is untouched — re-run to plan against " +
					"the file that is there now.",
			}
		}
		if err := c.deps.WriteConfig(path, patterns); err != nil {
			return err
		}
		fmt.Fprintf(out, "Wrote %s\n", path)
		res.ConfigWritten = path

	case p.pending(StepWireIncludeCron):
		if err := c.deps.WireCron(path, patterns); err != nil {
			return wireError(path, err)
		}
		fmt.Fprintf(out, "Added include_cron to %s\n", path)
		res.IncludeWired = true
	}
	return nil
}

// install hands off to autostart, which owns the ordering that makes this safe:
// unit first, cron masked second, RunWisp started third, and cron unmasked and
// restarted if that last step fails. installed is false when the operator
// declined at its prompt.
//
// PreConfirmed is set because the caller has already rendered a plan that is a
// superset of the installer's own banner and taken consent for it. Asking twice
// for one decision is how an operator learns to stop reading prompts.
func (c *Cutover) install(ctx context.Context, p Plan, out io.Writer) (installed, stale bool, err error) {
	if c.deps.Preflight != nil {
		stale, err = c.deps.Preflight(ctx)
		if err != nil {
			return false, false, err
		}
	}

	opts := p.Opts
	opts.TakeOverCron = p.MasksCron
	opts.PreConfirmed = true

	if err := c.deps.Installer.Install(ctx, opts, out); err != nil {
		if errors.Is(err, autostart.ErrAborted) {
			fmt.Fprintf(out, "Aborted.%s\n", cronUntouched(p))
			return false, stale, nil
		}
		if errors.Is(err, autostart.ErrConfigMissing) {
			// Reachable only if something removed the config between the write
			// above and here; the generic autostart wording would send the
			// operator off to scaffold a file RunWisp just made.
			return false, stale, &userError{
				title:   fmt.Sprintf("%s went missing mid-install", p.Opts.Config),
				details: "Nothing was masked. Re-run to start over.",
			}
		}
		return false, stale, err
	}
	return true, stale, nil
}

// cronUntouched reassures on an abort, but only when there was a cron unit to
// leave alone in the first place.
func cronUntouched(p Plan) string {
	if p.Evidence.CronUnit == "" {
		return ""
	}
	return fmt.Sprintf(" %s is untouched, and RunWisp holds the jobs it owns.", p.Evidence.CronUnit)
}

// wireError phrases a failed include_cron insertion. Every case here left the
// file byte-identical, which is the part the operator needs to hear first.
func wireError(path string, err error) error {
	var conflict *configedit.ConflictError
	if errors.As(err, &conflict) {
		return &userError{
			title: fmt.Sprintf("%s would not load with include_cron added — nothing was written", path),
			details: conflict.Err.Error() +
				"\n\nThe file was restored exactly as it was, and cron is untouched. A likely cause is a " +
				"task name that already exists: if you have run `runwisp import cron` before, those " +
				"imported copies collide with the live crontab jobs. Retire the crontab " +
				"(`crontab -r`) and keep the import, or remove the imported copies and keep the " +
				"live read — not both.",
		}
	}
	if errors.Is(err, configedit.ErrCronIncludeAlreadySet) {
		// Compute classifies this as a blocker, so reaching it here means the
		// config changed under us.
		return &userError{
			title:   fmt.Sprintf("%s gained an include_cron while RunWisp was asking", path),
			details: "Nothing was written. Re-run to plan against the config that is there now.",
		}
	}
	return err
}

// userError is this package's operator-facing error. It exists because
// cmd/runwisp's userFacingError is private to package main, and a decision this
// package makes has to be reportable without importing the CLI.
type userError struct {
	title   string
	details string
}

func (e *userError) Error() string {
	if e.details == "" {
		return e.title
	}
	return e.title + "\n\n" + e.details
}

// Title and Details let cmd/runwisp re-wrap this in its own error type, so the
// CLI keeps one rendering for every failure it prints.
func (e *userError) Title() string   { return e.title }
func (e *userError) Details() string { return e.details }
