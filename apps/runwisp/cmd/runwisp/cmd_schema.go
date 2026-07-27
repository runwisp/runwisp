// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"strings"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/spf13/cobra"
)

var schemaCmd = &cobra.Command{
	Use:   "schema",
	Short: "Print the runwisp.toml JSON Schema to stdout",
	Long: `Prints the JSON Schema (draft 2020-12) describing the full runwisp.toml
surface — every table, key, type, and default. It is the machine-readable
contract for the config file, the counterpart to ` + "`runwisp openapi`" + ` for the
REST API.

Point an editor at it for inline validation and completion (Even Better TOML /
taplo pick up a ` + "`#:schema`" + ` directive at the top of runwisp.toml), or feed it
to an agent so it can check a config before ` + "`runwisp validate`" + `. The same
schema is published at ` + config.SchemaURL + `.`,
	Example: strings.TrimSpace(`
  runwisp schema > runwisp.schema.json
  runwisp schema | jq '.["$defs"].task.properties | keys'`),
	RunE: func(cmd *cobra.Command, args []string) error {
		// The embedded schema already carries a trailing newline; print it as-is
		// so stdout is byte-identical to the published file.
		_, err := fmt.Fprint(cmd.OutOrStdout(), config.SchemaJSON())
		return err
	},
}
