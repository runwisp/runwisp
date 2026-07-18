// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/spf13/cobra"
)

var validateJSON bool

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate runwisp.toml without starting anything",
	Long:  `Parses and validates the configuration file. Prints a summary on success or a structured error on failure. Useful for CI pipelines and pre-commit checks.`,
	Example: `  runwisp validate
  runwisp validate -c ./deploy/runwisp.toml
  runwisp validate --json   # machine-readable; errors carry key/line/column`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runValidate(cmd.OutOrStdout(), flags, validateJSON)
	},
	SilenceErrors: true,
	SilenceUsage:  true,
}

func init() {
	validateCmd.Flags().BoolVar(&validateJSON, "json", false, "emit a machine-readable JSON document to stdout instead of the human summary")
}

// runValidate loads and validates f.CfgFile. On success it prints a
// short ✓ summary to w and returns nil; on failure it returns a userFacing
// error so main.go renders the message in the same style as other CLI
// errors. The summary covers the values an operator most often wants to
// double-check after editing the file: task / service counts and the
// resolved scheduler timezone (config-pinned vs system-detected).
//
// With asJSON, w receives a single validateJSONDoc instead (valid=false on
// failure, still returning the error so the exit code stays non-zero).
func runValidate(w io.Writer, f Flags, asJSON bool) error {
	cfg, err := config.Load(f.CfgFile)
	if err != nil {
		if asJSON {
			// Emit the error document to stdout so an agent never has to parse
			// text to know validation failed; the returned error still drives a
			// non-zero exit (and its human copy to stderr via main.go).
			if werr := writeJSON(w, validateJSONDoc{
				SchemaVersion: jsonSchemaVersion,
				Valid:         false,
				ConfigPath:    f.CfgFile,
				Warnings:      messagesFromStrings(nil),
				Errors:        messagesFromError(err),
			}); werr != nil {
				return werr
			}
		}
		return &userFacingError{
			title:   fmt.Sprintf("%s is not valid", f.CfgFile),
			details: err.Error(),
		}
	}

	tasks, services := 0, 0
	for i := range cfg.Tasks {
		if cfg.Tasks[i].Kind.IsService() {
			services++
		} else {
			tasks++
		}
	}

	if asJSON {
		return writeJSON(w, validateJSONDoc{
			SchemaVersion:  jsonSchemaVersion,
			Valid:          true,
			ConfigPath:     f.CfgFile,
			Timezone:       cfg.Scheduler.Timezone,
			TimezoneSource: cfg.Scheduler.Source,
			Tasks:          tasks,
			Services:       services,
			Warnings:       messagesFromStrings(config.Warnings(cfg)),
			Errors:         []messageJSON{},
		})
	}

	tz := cfg.Scheduler.Timezone
	if cfg.Scheduler.Source != "" {
		tz = fmt.Sprintf("%s (%s)", tz, cfg.Scheduler.Source)
	}

	fmt.Fprintf(w, "✓ %s is valid.\n", f.CfgFile)
	fmt.Fprintf(w, "  tasks:    %d\n", tasks)
	fmt.Fprintf(w, "  services: %d\n", services)
	fmt.Fprintf(w, "  timezone: %s\n", tz)
	// Advisory findings the daemon would log at boot — shown here so a CI
	// `validate` run surfaces them before deploy. They don't affect exit code.
	for _, warning := range config.Warnings(cfg) {
		fmt.Fprintf(w, "! %s\n", warning)
	}
	return nil
}
