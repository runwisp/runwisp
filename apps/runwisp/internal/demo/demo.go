// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package demo provides the embedded "Acme Notes" demo configuration and a
// seeder that populates a throwaway data directory with believable historical
// runs. It backs the `runwisp demo` command: boot a fully populated daemon
// (TUI + Web UI) without the operator writing any runwisp.toml.
package demo

import (
	_ "embed" // enables the //go:embed directive for ConfigTOML below
	"fmt"
	"os"
)

// ConfigTOML is the embedded demo runwisp.toml ("Acme Notes" persona), the
// single source of truth for the demo task set.
//
//go:embed runwisp.toml
var ConfigTOML []byte

// WriteConfig writes the embedded demo configuration to path with operator-only
// permissions, as a hand-authored runwisp.toml would sit on disk.
func WriteConfig(path string) error {
	if err := os.WriteFile(path, ConfigTOML, 0600); err != nil {
		return fmt.Errorf("write demo config: %w", err)
	}
	return nil
}
