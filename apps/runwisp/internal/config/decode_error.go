// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/runwisp/runwisp/apps/runwisp/internal/textutil"
)

// LocatedError is a config error that carries a structured source location
// alongside its human message. `runwisp validate --json` surfaces these fields
// so an agent can jump straight to the offending site instead of parsing the
// prose. Line/Column are 1-based TOML source positions (0 = unknown); Key is
// the dotted key path (e.g. "tasks.backup.cron") when the parser knows it.
//
// Only parse-time errors (malformed TOML, unknown keys, type mismatches) carry
// a location today — that is the class an agent hits editing runwisp.toml by
// hand. Semantic validation errors remain message-only.
type LocatedError struct {
	Msg    string
	Key    string
	Line   int
	Column int
}

func (e *LocatedError) Error() string { return e.Msg }

// keyPath renders a go-toml key as a dotted path, e.g. tasks.backup.cron.
func keyPath(key toml.Key) string {
	return strings.Join([]string(key), ".")
}

// formatDecodeError converts pelletier's strict-mode and type errors into
// messages an operator can read, wrapped in a LocatedError so the source
// position travels with the message. The strict-mode message in the upstream
// library doesn't surface the field name; we walk the wrapped DecodeError
// list to pull it out.
func formatDecodeError(err error, located bool) error {
	var strict *toml.StrictMissingError
	if errors.As(err, &strict) {
		// Surface every unknown key as its own located error so `validate --json`
		// reports all offending sites, not just the first. Each message carries
		// its own line/column/key.
		if len(strict.Errors) > 0 {
			errs := make([]error, 0, len(strict.Errors))
			for i := range strict.Errors {
				de := strict.Errors[i]
				row, col := position(de, located)
				errs = append(errs, &LocatedError{
					Msg:    fmt.Sprintf("failed to parse config file:%s", formatOneStrictMissing(de, located)),
					Key:    keyPath(de.Key()),
					Line:   row,
					Column: col,
				})
			}
			return errors.Join(errs...)
		}
		return &LocatedError{Msg: "failed to parse config file:"}
	}
	var decode *toml.DecodeError
	if errors.As(err, &decode) {
		row, col := position(*decode, located)
		msg := fmt.Sprintf("failed to parse config file: %s", decode.Error())
		if !located && len(decode.Key()) > 0 {
			msg += " at " + keyPath(decode.Key())
		}
		return &LocatedError{
			Msg:    msg,
			Key:    keyPath(decode.Key()),
			Line:   row,
			Column: col,
		}
	}
	return fmt.Errorf("failed to parse config file: %w", err)
}

// position is the error's line and column, or 0, 0 when they point into TOML
// the operator never saw.
func position(de toml.DecodeError, located bool) (row, col int) {
	if !located {
		return 0, 0
	}
	return de.Position()
}

// formatOneStrictMissing renders a single unknown-key decode error, including
// the leading "\n  " indent, a did-you-mean suggestion, and a section hint when
// available.
func formatOneStrictMissing(de toml.DecodeError, located bool) string {
	key := de.Key()
	field := keyTail(key)
	var candidates []string
	// Prefer the segment that actually failed to match: for a typo'd
	// table name ([taks.foo]) the tail is "foo" but the problem is
	// "taks". The walker also yields the valid keys at that level for
	// a did-you-mean hint.
	if seg, cands, ok := unknownKeyInfo(key); ok {
		field, candidates = seg, cands
	}
	at := keyPath(key)
	if located {
		row, col := de.Position()
		at = fmt.Sprintf("line %d:%d", row, col)
	}
	var b strings.Builder
	b.WriteString("\n  ")
	if field != "" {
		fmt.Fprintf(&b, "unknown key %q at %s", field, at)
		if suggestion := textutil.Closest(field, candidates); suggestion != "" {
			fmt.Fprintf(&b, " (did you mean %q?)", suggestion)
		}
		if hint := sectionHint(key); hint != "" {
			fmt.Fprintf(&b, " — %s", hint)
		}
	} else {
		b.WriteString(de.Error())
	}
	return b.String()
}

func keyTail(key toml.Key) string {
	if len(key) == 0 {
		return ""
	}
	return key[len(key)-1]
}
