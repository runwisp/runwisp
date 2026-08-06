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
	"regexp"
)

// ConfigTOML is the embedded demo runwisp.toml ("Acme Notes" persona), the
// single source of truth for the demo task set.
//
//go:embed runwisp.toml
var ConfigTOML []byte

// externalURLLine matches the demo config's [daemon] external_url setting, so
// WriteConfig can drop it for the interactive demo (see below).
var externalURLLine = regexp.MustCompile(`(?m)^external_url\s*=.*\n`)

// WriteConfig writes the embedded demo configuration to path with operator-only
// permissions, as a hand-authored runwisp.toml would sit on disk.
//
// The embedded config pins external_url to a fictional domain so the
// screenshot/video tooling (which calls WriteConfig via `demo --seed-only`)
// shows a believable operator setup. The interactive `runwisp demo` command
// wants the opposite: "Open Web UI" should reach the real throwaway daemon, so
// keepExternalURL is false there and this strips the line, letting WebURL()
// fall back to the daemon's actual bind host:port.
func WriteConfig(path string, keepExternalURL bool) error {
	toml := ConfigTOML
	if !keepExternalURL {
		toml = externalURLLine.ReplaceAll(toml, nil)
	}
	if err := os.WriteFile(path, toml, 0600); err != nil {
		return fmt.Errorf("write demo config: %w", err)
	}
	return nil
}
