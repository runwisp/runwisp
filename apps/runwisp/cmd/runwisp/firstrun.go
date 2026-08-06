// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/configedit"
	"github.com/runwisp/runwisp/internal/cutover"
)

// scaffoldIfMissing checks for a runwisp.toml at f.CfgFile. If it is absent and
// stdin is attached to a terminal, prompts the operator to create a starter
// config and writes one on confirmation. Non-TTY callers (background daemon,
// systemd, Docker) are a no-op — the daemon falls back to the built-in demo
// task and emits a warning.
//
// installed reports that the first run also installed and started the system
// service (the cron cutover), so the caller must attach to that daemon rather
// than spawning one of its own.
func scaffoldIfMissing(f Flags) (installed bool, err error) {
	if _, err := os.Stat(f.CfgFile); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		return false, nil
	}
	return promptAndScaffold(f, os.Stdin, os.Stderr)
}

// configLocation returns a human-readable location for path. When the file has
// the default name "runwisp.toml", the directory is returned to avoid the
// redundant "No runwisp.toml at runwisp.toml" phrasing.
func configLocation(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if filepath.Base(abs) == "runwisp.toml" {
		return filepath.Dir(abs)
	}
	return abs
}

func promptAndScaffold(f Flags, in io.Reader, out io.Writer) (installed bool, err error) {
	path := f.CfgFile
	loc := configLocation(path)
	fmt.Fprintf(out, "No runwisp.toml at %s.\n", loc)

	composeFile, composeAlias, hasCompose := detectAdjacentCompose(path)
	if hasCompose {
		fmt.Fprintf(out, "Detected %s alongside.\n", composeFile)
	}

	scan, hasCron := scanForCron(path)
	for _, reason := range scan.Blocked {
		fmt.Fprintf(out, "Found a cron source RunWisp can't read yet: %s\n", reason)
	}

	// The cutover is only ever offered alongside crontabs to read: masking cron
	// on a box RunWisp is not reading is how you stop every job on it.
	var offer *firstRunCutover
	if hasCron {
		offer = offerFirstRunCutover(f, out, cronScaffoldWriter(composeFile, composeAlias, hasCompose))
	}

	switch {
	case hasCron:
		fmt.Fprintf(out, "Found %d cron job(s) on this box (%s).\n",
			scan.Jobs, strings.Join(cutover.DescribeSources(scan.Files), ", "))
		switch {
		case offer != nil:
			fmt.Fprintf(out, "%s [Y/n] ", cutover.DescribeOffer(offer.plan))
		case hasCompose:
			fmt.Fprint(out, heldNote)
			fmt.Fprint(out, "Create a starter that imports the compose services and reads these live? [Y/n] ")
		default:
			fmt.Fprint(out, heldNote)
			fmt.Fprint(out, "Read them as RunWisp tasks? [Y/n] ")
		}
	case hasCompose:
		fmt.Fprint(out, "Create a starter that imports its services? [Y/n] ")
	default:
		fmt.Fprint(out, "Create a starter with one example task? [Y/n] ")
	}

	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, fmt.Errorf("read prompt response: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "", "y", "yes":
	default:
		return false, fmt.Errorf("no runwisp.toml at %s — create one and try again (docs: https://docs.runwisp.com/configuration/overview/)", loc)
	}

	if offer != nil {
		return offer.perform(out)
	}

	if err := writeScaffold(path, composeFile, composeAlias, hasCompose, scan.Globs, hasCron); err != nil {
		return false, fmt.Errorf("write starter: %w", err)
	}
	fmt.Fprintf(out, "Created %s\n", path)
	return false, nil
}

// heldNote is the truthful version of the offer for a host that cannot retire
// cron itself — non-root, no systemd, macOS's SIP-protected cron, or any other
// cutover blocker. Saying nothing here is what made the old prompt misleading:
// RunWisp reads the jobs but cron keeps running them.
const heldNote = "cron keeps running them for now, so RunWisp holds those jobs — nothing fires twice.\n" +
	"Run 'sudo runwisp takeover' when you want RunWisp to own them (needs root and systemd).\n"

// firstRunCutover is a cron take-over this host can actually perform, paired with
// the plan the operator is being shown. Both halves are built before the prompt:
// the plan has to name the real cron unit it would mask, and writing the config is
// the plan's own first step — so nothing can be scaffolded until the answer is in.
type firstRunCutover struct {
	c    *cutover.Cutover
	plan cutover.Plan
}

// offerFirstRunCutover is the seam the first-run prompt consults. The default
// implementation probes the real host — systemd, the real executable path, the
// real euid — so tests substitute it, exactly like scanForCron.
var offerFirstRunCutover = probeFirstRunCutover

// probeFirstRunCutover returns the offer, or nil when this box would not benefit
// from one. Every nil is a non-error: the operator did not ask for a take-over, so
// a host that cannot do one just gets the plain scaffold prompt plus heldNote,
// which already names `sudo runwisp takeover` and what it needs. Printing the
// blockers here instead would greet every ordinary laptop first run with a wall of
// text about root and systemd.
//
// writeConfig is threaded in so the plan's config step writes the *right*
// scaffold: a directory with both crontabs and a docker-compose.yml gets both, off
// the one question already on screen.
func probeFirstRunCutover(f Flags, out io.Writer, writeConfig func(path string, patterns []string) error) *firstRunCutover {
	deps, err := autostart.DefaultDeps(out, os.Stdin, false)
	if err != nil {
		return nil
	}
	// No terminal, no question — and a take-over is not something to assume.
	if !deps.StdinIsTTY {
		return nil
	}
	installer, err := autostart.New(deps)
	if err != nil {
		return nil
	}
	binary, err := resolveUnitBinary(out, deps, "")
	if err != nil {
		return nil
	}

	// Built straight from Flags rather than through resolveServiceOptions: that one
	// reads cobra's Flag().Changed to tell an explicit --data/--config from a
	// default, and first run has no command to ask. It does not need to — root's
	// path defaults (/etc/runwisp/runwisp.toml, /var/lib/runwisp, resolvePathDefaults
	// in root.go) are already exactly what a system install would pick.
	c := newCutover(f, deps, installer, autostart.InstallOptions{
		Binary:  binary,
		Config:  f.CfgFile,
		DataDir: f.DataDir,
		Host:    f.Host,
		Port:    f.Port,
		System:  true,
	}, cutover.Options{}, func(d *cutover.Deps) { d.WriteConfig = writeConfig })

	// This re-scans the crontabs scanForCron just looked at. Cheap (a glob and a
	// few small files), and worth it: the plan owns its own evidence, so what the
	// prompt describes is what Execute will act on.
	plan, err := c.Compute(context.Background())
	if err != nil {
		return nil
	}
	// Only offer when cron is live. An inactive cron unit is nothing to retire —
	// the jobs are not held and nothing is firing twice — and a blocked plan is a
	// take-over this box cannot do at all.
	if plan.Blocked() || !plan.MasksCron || !plan.Evidence.CronActive {
		return nil
	}
	return &firstRunCutover{c: c, plan: plan}
}

// perform runs the offered cutover: config first, then the unit, then cron. It
// reports whether a service ended up installed and started, so the caller knows
// to attach to that daemon rather than spawn one of its own.
//
// An abort at the installer's own prompt is not an error — the config is on disk
// and cron is untouched, so the first run carries on with its own daemon.
func (o *firstRunCutover) perform(out io.Writer) (installed bool, err error) {
	res, err := o.c.Execute(context.Background(), o.plan, out)
	if err != nil {
		if res.ConfigWritten != "" {
			fmt.Fprintf(out, "\n%s was created and cron is untouched — fix the above, then run "+
				"'sudo runwisp takeover'.\n", res.ConfigWritten)
		}
		return false, asUserFacing(err)
	}
	return res.ServiceInstalled, nil
}

// cronScaffoldWriter adapts writeScaffold to internal/cutover's WriteConfig seam.
// hasCron is true by construction: this writer only exists on the path where
// crontabs were found.
func cronScaffoldWriter(composeFile, composeAlias string, hasCompose bool) func(string, []string) error {
	return func(path string, patterns []string) error {
		if err := writeScaffold(path, composeFile, composeAlias, hasCompose, patterns, true); err != nil {
			return fmt.Errorf("write starter: %w", err)
		}
		return nil
	}
}

// writeScaffold picks the one starter template matching what was detected.
// Compose and cron are not exclusive — a directory can have both an adjacent
// compose file and readable crontabs — so all four combinations get a path.
func writeScaffold(path, composeFile, composeAlias string, hasCompose bool, cronPatterns []string, hasCron bool) error {
	switch {
	case hasCompose && hasCron:
		return configedit.WriteInitWithComposeAndCron(path, composeFile, composeAlias, cronPatterns)
	case hasCron:
		return configedit.WriteInitWithCron(path, cronPatterns)
	case hasCompose:
		return configedit.WriteInitWithCompose(path, composeFile, composeAlias)
	default:
		return configedit.WriteInit(path)
	}
}

// detectAdjacentCompose searches the directory containing path for the
// first compose file matching config.ComposeAutoDiscoveryFilenames.
// Returns the filename, a sanitized alias derived from the directory name,
// and true when one is found.
func detectAdjacentCompose(path string) (filename, alias string, ok bool) {
	dir := filepath.Dir(path)
	for _, name := range config.ComposeAutoDiscoveryFilenames {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return name, composeAliasFromDir(dir), true
		}
	}
	return "", "", false
}

// composeAliasFromDir derives a [compose.<alias>] key from a directory
// path. The directory's base name is sanitized to the TaskNamePattern
// charset; a "." or empty result falls back to "myapp" so the scaffold
// is always loadable.
func composeAliasFromDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	base := filepath.Base(abs)
	var b strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-_")
	if out == "" || out == "." {
		return "myapp"
	}
	return out
}

// scanForCron scans for real crontabs this account can read — the cron
// counterpart of detectAdjacentCompose. ok is false when nothing was found and
// when *any* matched source was blocked outright (untrusted, or one this account
// can't become the owner of): an include_cron whose first load already reports a
// refusal is worse than not offering one, and the operator has a better next
// step (fix the file, or `runwisp import cron`) than a config that half-works.
// Either way, scan.Blocked comes back so the caller can explain why nothing was
// offered.
//
// A package-level var, like the codebase's other OS-probing seams
// (cronUserExists, cronServiceProbe): it touches the real filesystem, which
// unit tests for promptAndScaffold must not do — a CI image with real crontabs
// under /etc/cron.d, or one running as root, would otherwise make the first-run
// prompt tests flaky and machine-dependent. Tests override this var directly.
var scanForCron = func(cfgPath string) (scan config.CronScan, ok bool) {
	patterns := config.DefaultCronPatterns(os.Geteuid(), currentUsername())
	scan = config.ScanCronSources(patterns, cfgPath)
	return scan, scan.Jobs > 0 && len(scan.Blocked) == 0
}

// currentUsername looks up the account running this process, for
// DefaultCronPatterns' unprivileged branch. "" (rather than an error) tells
// the caller to skip cron detection entirely — there is no safe pattern to
// offer without a name to match a spool file against.
func currentUsername() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.Username
}
