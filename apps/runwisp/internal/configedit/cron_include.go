// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package configedit

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
)

// ErrCronIncludeAlreadySet signals that the root config already declares a
// [daemon].include_cron the operator maintains. RunWisp will not rewrite such a
// list, even to add to it, so the caller must refuse cleanly (writing nothing)
// and tell the operator what to add. An explicit `include_cron = []` counts:
// that is someone saying "read no crontabs", which is an answer.
var ErrCronIncludeAlreadySet = errors.New("daemon.include_cron is already set")

// EnsureCronInclude returns rootTOML with patterns wired into
// [daemon].include_cron. Like EnsureStagingInclude it is a surgical text edit
// that never reformats or reorders the operator's existing bytes:
//
//   - if there is a [daemon] table but no include_cron key, the key is inserted
//     right after the header;
//   - if there is no [daemon] table, a fresh one is appended;
//   - if include_cron is still the array RunWisp scaffolded (byte for byte, and
//     only patterns from this same list), the missing patterns are added to it,
//     so a newer RunWisp's take-over can read a source an older one didn't;
//   - any other include_cron is the operator's: ErrCronIncludeAlreadySet.
//
// added is what the edit adds, for the caller to name in its plan.
//
// Unlike its staging counterpart there is no "already covered, nothing to do"
// case, and deliberately no coverage check at all. Whether a config actually
// reads any crontabs is answered once, authoritatively, by loading it and
// asking Config.CronFiles() — the callers do that before getting here. A second
// glob-matching implementation living in this package could only ever disagree
// with the loader.
func EnsureCronInclude(rootTOML []byte, patterns []string) (out []byte, added []string, err error) {
	if len(patterns) == 0 {
		return nil, nil, errors.New("no include_cron patterns to wire")
	}

	var probe struct {
		Daemon struct {
			IncludeCron []string `toml:"include_cron"`
		} `toml:"daemon"`
	}
	if err := toml.Unmarshal(rootTOML, &probe); err != nil {
		return nil, nil, fmt.Errorf("parse root config: %w", err)
	}
	declared := probe.Daemon.IncludeCron
	// nil means the key is absent.
	if declared == nil {
		return insertDaemonKey(rootTOML, config.CronIncludeArray(patterns)), patterns, nil
	}
	return extendScaffoldedInclude(rootTOML, declared, patterns)
}

// extendScaffoldedInclude adds the missing patterns to an include_cron RunWisp
// wrote itself. "Wrote itself" is proven, not guessed: the array is non-empty,
// lists nothing outside patterns, and its text is still exactly what
// config.CronIncludeArray rendered. A reformatted array, a path the operator
// added, or an empty one is theirs.
func extendScaffoldedInclude(rootTOML []byte, declared, patterns []string) ([]byte, []string, error) {
	text := string(rootTOML)
	scaffolded := config.CronIncludeArray(declared)
	if len(declared) == 0 || strings.Count(text, scaffolded) != 1 ||
		slices.ContainsFunc(declared, func(p string) bool { return !slices.Contains(patterns, p) }) {
		return nil, nil, ErrCronIncludeAlreadySet
	}
	var added []string
	for _, p := range patterns {
		if !slices.Contains(declared, p) {
			added = append(added, p)
		}
	}
	if len(added) == 0 {
		return rootTOML, nil, nil
	}
	return []byte(strings.Replace(text, scaffolded, config.CronIncludeArray(patterns), 1)), added, nil
}

// WireCronInclude applies EnsureCronInclude to the config at path and writes it
// back, gated on the result actually loading. A config that wouldn't load is
// rolled back byte-for-byte, so a failed wiring never leaves the operator worse
// off than before — which matters here more than usual, because the caller's very
// next move is to mask cron.
func WireCronInclude(path string, patterns []string) error {
	orig, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	wired, _, err := EnsureCronInclude(orig, patterns)
	if err != nil {
		return err
	}

	txn := New()
	txn.Write(path, wired, DefaultPerm)
	return txn.Apply(loadGate(path))
}
