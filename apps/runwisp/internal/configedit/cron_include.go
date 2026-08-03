// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package configedit

import (
	"errors"
	"fmt"
	"os"

	"github.com/pelletier/go-toml/v2"
	"github.com/runwisp/runwisp/internal/config"
)

// ErrCronIncludeAlreadySet signals that the root config already declares a
// [daemon].include_cron. RunWisp will not rewrite a crontab list the operator
// maintains — even to add to it — so the caller must refuse cleanly (writing
// nothing) and tell the operator what to add. An explicit `include_cron = []`
// counts: that is someone saying "read no crontabs", which is an answer.
var ErrCronIncludeAlreadySet = errors.New("daemon.include_cron is already set")

// EnsureCronInclude returns rootTOML with patterns wired into
// [daemon].include_cron. Like EnsureStagingInclude it is a surgical text edit
// that never reformats or reorders the operator's existing bytes:
//
//   - if there is a [daemon] table but no include_cron key, the key is inserted
//     right after the header;
//   - if there is no [daemon] table, a fresh one is appended;
//   - if include_cron is already declared, it returns ErrCronIncludeAlreadySet
//     rather than touch the operator's array.
//
// Unlike its staging counterpart there is no "already covered, nothing to do"
// case, and deliberately no coverage check at all. Whether a config actually
// reads any crontabs is answered once, authoritatively, by loading it and
// asking Config.CronFiles() — the callers do that before getting here. A second
// glob-matching implementation living in this package could only ever disagree
// with the loader.
func EnsureCronInclude(rootTOML []byte, patterns []string) (out []byte, err error) {
	if len(patterns) == 0 {
		return nil, errors.New("no include_cron patterns to wire")
	}

	var probe struct {
		Daemon struct {
			IncludeCron []string `toml:"include_cron"`
		} `toml:"daemon"`
	}
	if err := toml.Unmarshal(rootTOML, &probe); err != nil {
		return nil, fmt.Errorf("parse root config: %w", err)
	}
	// nil means the key is absent; an empty-but-present array is the operator's.
	if probe.Daemon.IncludeCron != nil {
		return nil, ErrCronIncludeAlreadySet
	}

	text := ensureTrailingNewline(string(rootTOML))
	key := config.CronIncludeArray(patterns)
	if end, ok := daemonHeaderEnd(text); ok {
		return []byte(text[:end] + key + text[end:]), nil
	}
	return []byte(text + "\n[daemon]\n" + key), nil
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
	wired, err := EnsureCronInclude(orig, patterns)
	if err != nil {
		return err
	}

	txn := New()
	txn.Write(path, wired, DefaultPerm)
	return txn.Apply(func() error {
		if _, err := config.Load(path); err != nil {
			return &ConflictError{Err: err}
		}
		return nil
	})
}
