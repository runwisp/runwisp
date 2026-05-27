// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/runwisp/runwisp/internal/config"
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
		fmt.Fprint(out, "Create a starter that imports its services? [Y/n] ")
	} else {
		fmt.Fprint(out, "Create a starter with one example task? [Y/n] ")
	}

	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read prompt response: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "", "y", "yes":
		if hasCompose {
			if err := config.WriteInitWithCompose(path, composeFile, composeAlias); err != nil {
				return fmt.Errorf("write starter: %w", err)
			}
		} else if err := config.WriteInit(path); err != nil {
			return fmt.Errorf("write starter: %w", err)
		}
		fmt.Fprintf(out, "Created %s\n", path)
		return nil
	default:
		return fmt.Errorf("no runwisp.toml at %s — create one and try again (docs: https://github.com/runwisp/runwisp)", loc)
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
