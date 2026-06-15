// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/runwisp/runwisp/internal/textutil"
)

// formatDecodeError converts pelletier's strict-mode and type errors into
// messages an operator can read. The strict-mode message in the upstream
// library doesn't surface the field name; we walk the wrapped DecodeError
// list to pull it out.
func formatDecodeError(err error) error {
	var strict *toml.StrictMissingError
	if errors.As(err, &strict) {
		return fmt.Errorf("failed to parse config file:%s", formatStrictMissing(strict))
	}
	var decode *toml.DecodeError
	if errors.As(err, &decode) {
		return fmt.Errorf("failed to parse config file: %s", decode.Error())
	}
	return fmt.Errorf("failed to parse config file: %w", err)
}

func formatStrictMissing(s *toml.StrictMissingError) string {
	var b strings.Builder
	for _, de := range s.Errors {
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
		row, col := de.Position()
		b.WriteString("\n  ")
		if field != "" {
			fmt.Fprintf(&b, "unknown key %q at line %d:%d", field, row, col)
			if suggestion := textutil.Closest(field, candidates); suggestion != "" {
				fmt.Fprintf(&b, " (did you mean %q?)", suggestion)
			}
			if hint := sectionHint(key); hint != "" {
				fmt.Fprintf(&b, " — %s", hint)
			}
		} else {
			b.WriteString(de.Error())
		}
	}
	return b.String()
}

func keyTail(key toml.Key) string {
	if len(key) == 0 {
		return ""
	}
	return key[len(key)-1]
}
