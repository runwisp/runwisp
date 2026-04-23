// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/spf13/cobra"
	"log/slog"
)

var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate runwisp.yaml without starting anything",
	Long:  `Parses and validates the configuration file, reporting any errors. Useful for CI pipelines and pre-commit checks.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runValidate()
	},
}

func runValidate() error {
	cfg, err := config.Load(flags.CfgFile)
	if err != nil {
		return fmt.Errorf("failed to load %s: %w", flags.CfgFile, err)
	}

	if err := config.Validate(cfg); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	slog.Info("Configuration is valid", "path", flags.CfgFile, "tasks", len(cfg.Tasks))
	return nil
}
