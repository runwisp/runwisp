// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/configedit"
	"github.com/runwisp/runwisp/internal/importer"
)

// scaffoldIfMissing checks for a runwisp.toml at path. If it is absent and
// stdin is attached to a terminal, prompts the operator to create a starter
// config and writes one on confirmation. Non-TTY callers (background daemon,
// systemd, Docker) are a no-op — the daemon falls back to the built-in demo
// task and emits a warning.
func scaffoldIfMissing(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !isatty.IsTerminal(os.Stdin.Fd()) {
		return nil
	}
	return promptAndScaffold(path, os.Stdin, os.Stderr)
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

func promptAndScaffold(path string, in io.Reader, out io.Writer) error {
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

	switch {
	case hasCron:
		fmt.Fprintf(out, "Found %d cron job(s) on this box (%s).\n",
			scan.Jobs, strings.Join(describeCronSources(scan.Files), ", "))
		if hasCompose {
			fmt.Fprint(out, "Create a starter that imports the compose services and reads these live? [Y/n] ")
		} else {
			fmt.Fprint(out, "Read them as RunWisp tasks? [Y/n] ")
		}
	case hasCompose:
		fmt.Fprint(out, "Create a starter that imports its services? [Y/n] ")
	default:
		fmt.Fprint(out, "Create a starter with one example task? [Y/n] ")
	}

	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read prompt response: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "", "y", "yes":
		if err := writeScaffold(path, composeFile, composeAlias, hasCompose, scan.Globs, hasCron); err != nil {
			return fmt.Errorf("write starter: %w", err)
		}
		fmt.Fprintf(out, "Created %s\n", path)
		return nil
	default:
		return fmt.Errorf("no runwisp.toml at %s — create one and try again (docs: https://docs.runwisp.com/configuration/overview/)", loc)
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
// counterpart of detectAdjacentCompose. ok is false both when nothing was
// found and when every matched source was blocked outright (untrusted, or
// one this account can't become the owner of): scaffolding an include_cron
// that can't schedule anything on its first load would be worse than not
// offering it. Either way, scan.Blocked comes back so the caller can explain
// why nothing was offered.
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

// describeCronSources renders the matched cron files as a short, friendly
// list for the first-run prompt: every file under a cron.d directory
// collapses to one mention of the directory, and a spool file names its
// owner rather than its path.
func describeCronSources(files []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, f := range files {
		switch {
		case f == importer.SystemCrontabPath:
			add(f)
		case importer.IsSystemCrontabPath(f):
			add(filepath.Dir(f))
		default:
			if owner, ok := importer.UserSpoolOwner(f); ok {
				add(owner + "'s crontab")
			} else {
				add(f)
			}
		}
	}
	return out
}
