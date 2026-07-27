// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package configedit

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/runwisp/runwisp/internal/config"
)

// ErrIncludeNeedsManualWiring signals that the root config already declares a
// [daemon].include that does not cover the staging file. RunWisp will not
// rewrite an operator's hand-maintained include list, so the caller must refuse
// cleanly (writing nothing) and tell the operator what to add.
var ErrIncludeNeedsManualWiring = errors.New("daemon.include needs manual wiring")

// EnsureStagingInclude returns rootTOML with the runwisp.d staging glob wired into
// [daemon].include, plus whether a change was needed. It is a surgical text edit
// that never reformats or reorders the operator's existing bytes:
//
//   - if the existing include already covers the staging file, the input is
//     returned unchanged (changed=false);
//   - if there is a [daemon] table but no include key, an include line is
//     inserted right after the header;
//   - if there is no [daemon] table, a fresh one is appended;
//   - if the root declares a custom include that doesn't cover staging, it
//     returns ErrIncludeNeedsManualWiring rather than touch the operator's array.
//
// rootDir is the directory the root config lives in, used to resolve include
// patterns and locate the staging file.
func EnsureStagingInclude(rootTOML []byte, rootDir string) (out []byte, changed bool, err error) {
	staging := config.StagingFilePath(rootDir)

	var probe struct {
		Daemon struct {
			Include []string `toml:"include"`
		} `toml:"daemon"`
	}
	if err := toml.Unmarshal(rootTOML, &probe); err != nil {
		return nil, false, fmt.Errorf("parse root config: %w", err)
	}

	if includeCovers(probe.Daemon.Include, rootDir, staging) {
		return rootTOML, false, nil
	}
	// A present-but-non-covering include (nil means the key is absent) is the
	// operator's own list — don't rewrite it.
	if probe.Daemon.Include != nil {
		return nil, false, ErrIncludeNeedsManualWiring
	}

	text := ensureTrailingNewline(string(rootTOML))
	if end, ok := daemonHeaderEnd(text); ok {
		return []byte(text[:end] + stagingIncludeLine() + text[end:]), true, nil
	}
	return []byte(text + "\n[daemon]\n" + stagingIncludeLine()), true, nil
}

// stagingIncludeLine is the include declaration wired into the root config.
func stagingIncludeLine() string {
	return fmt.Sprintf("include = [%q]\n", config.StagingIncludeGlob)
}

// includeCovers reports whether any of the include patterns would match the
// staging file, resolving relative patterns against rootDir. It matches on
// patterns rather than the filesystem so coverage can be decided before the
// staging file exists.
func includeCovers(patterns []string, rootDir, stagingAbs string) bool {
	for _, p := range patterns {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(rootDir, p)
		}
		if resolved, err := filepath.Abs(abs); err == nil {
			abs = resolved
		}
		if ok, _ := filepath.Match(abs, stagingAbs); ok {
			return true
		}
	}
	return false
}

// daemonHeaderEnd returns the byte offset just past the `[daemon]` table header
// line (including its newline), so an inserted key becomes the table's first
// entry. ok is false when there is no `[daemon]` header. Lines inside a
// multi-line string are skipped, so a `run = """ … [daemon] … """` body can't
// lure the insertion into the middle of a value.
func daemonHeaderEnd(text string) (int, bool) {
	for _, ln := range scanTOMLLines(text) {
		if !ln.Code {
			continue
		}
		if name, array, ok := tableHeader(ln.Text); ok && !array && name == "daemon" {
			return ln.End, true
		}
	}
	return 0, false
}

// ensureTrailingNewline appends a newline when text doesn't end with one, so
// surgical insertions never fuse two lines. An empty string is left empty.
func ensureTrailingNewline(text string) string {
	if text == "" || strings.HasSuffix(text, "\n") {
		return text
	}
	return text + "\n"
}
