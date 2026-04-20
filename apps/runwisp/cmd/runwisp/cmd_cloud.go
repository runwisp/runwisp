// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"github.com/spf13/cobra"
)

var cloudFlags struct {
	Token   string
	URL     string
	EnvFile string
}

var cloudCmd = &cobra.Command{
	Use:   "cloud",
	Short: "Start in cloud mode",
	Long: `Starts the daemon in cloud mode, connecting to the RunWisp cloud platform.
Requires RUNWISP_CLOUD_TOKEN to be set (via environment or .env file).
The local scheduler is not started — task scheduling is managed by the cloud.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := loadCloudEnvFile(cmd); err != nil {
			return err
		}

		if cloudFlags.Token != "" {
			os.Setenv("RUNWISP_CLOUD_TOKEN", cloudFlags.Token)
		}
		if cloudFlags.URL != "" {
			os.Setenv("RUNWISP_CLOUD_URL", cloudFlags.URL)
		}

		if os.Getenv("RUNWISP_CLOUD_TOKEN") == "" {
			return fmt.Errorf("RUNWISP_CLOUD_TOKEN is required — set it via environment, .env file, or --token flag")
		}

		return runDaemon(modeCloud)
	},
}

func init() {
	cloudCmd.Flags().StringVar(&cloudFlags.Token, "token", "", "cloud token (overrides RUNWISP_CLOUD_TOKEN)")
	cloudCmd.Flags().StringVar(&cloudFlags.URL, "url", "", "cloud API URL (overrides RUNWISP_CLOUD_URL)")
	cloudCmd.Flags().StringVar(&cloudFlags.EnvFile, "env-file", ".env", "path to .env file for cloud configuration")
	cloudCmd.Flags().BoolVar(&noTUI, "no-tui", false, "run in headless daemon mode (no interactive TUI)")
}

// loadCloudEnvFile loads variables from the .env file into the process
// environment. If --env-file was explicitly set and the file is missing, an
// error is returned. If using the default ".env" and the file is missing, the
// function silently continues.
func loadCloudEnvFile(cmd *cobra.Command) error {
	envFileExplicit := cmd.Flags().Changed("env-file")
	err := godotenv.Load(cloudFlags.EnvFile)
	if err == nil {
		return nil
	}
	if os.IsNotExist(err) && !envFileExplicit {
		return nil
	}
	if envFileExplicit {
		return fmt.Errorf("cannot load env file %q: %w", cloudFlags.EnvFile, err)
	}
	return fmt.Errorf("cannot load env file %q: %w", cloudFlags.EnvFile, err)
}
