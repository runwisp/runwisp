// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/spf13/cobra"
)

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate runwisp.toml without starting anything",
	Long:  `Parses and validates the configuration file. Prints a summary on success or a structured error on failure. Useful for CI pipelines and pre-commit checks.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runValidate(cmd.OutOrStdout())
	},
	SilenceErrors: true,
	SilenceUsage:  true,
}

// runValidate loads and validates flags.CfgFile. On success it prints a
// short ✓ summary to w and returns nil; on failure it returns a userFacing
// error so main.go renders the message in the same style as other CLI
// errors. The summary covers the values an operator most often wants to
// double-check after editing the file: task / service counts and the
// resolved scheduler timezone (config-pinned vs system-detected).
func runValidate(w io.Writer) error {
	cfg, err := config.Load(flags.CfgFile)
	if err != nil {
		return &userFacingError{
			title:   fmt.Sprintf("%s is not valid", flags.CfgFile),
			details: err.Error(),
		}
	}

	tz := cfg.Scheduler.Timezone
	if cfg.Scheduler.Source != "" {
		tz = fmt.Sprintf("%s (%s)", tz, cfg.Scheduler.Source)
	}

	tasks, services := 0, 0
	for i := range cfg.Tasks {
		if cfg.Tasks[i].Kind.IsService() {
			services++
		} else {
			tasks++
		}
	}

	fmt.Fprintf(w, "✓ %s is valid.\n", flags.CfgFile)
	fmt.Fprintf(w, "  tasks:    %d\n", tasks)
	fmt.Fprintf(w, "  services: %d\n", services)
	fmt.Fprintf(w, "  timezone: %s\n", tz)
	return nil
}
