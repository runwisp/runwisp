// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// renames maps each retired key to its replacement. Used when a TOML strict-
// mode error names a key the operator used to know — instead of "fields in
// the document are missing in the target struct" the error becomes
// "unknown key 'parallelism' for tasks; did you mean 'max_concurrent'?".
var renames = map[string]string{
	"parallelism": "max_concurrent",
}

// formatDecodeError converts pelletier's strict-mode and type errors into
// messages an operator can read. The strict-mode message in the upstream
// library doesn't surface the field name; we walk the wrapped DecodeError
// list to pull it out and add a "did you mean ..." hint where we recognise
// the legacy key.
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
		row, col := de.Position()
		b.WriteString("\n  ")
		if field != "" {
			b.WriteString(fmt.Sprintf("unknown key %q at line %d:%d", field, row, col))
			if hint, ok := renames[strings.ToLower(field)]; ok {
				b.WriteString(fmt.Sprintf(" — did you mean %q?", hint))
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
