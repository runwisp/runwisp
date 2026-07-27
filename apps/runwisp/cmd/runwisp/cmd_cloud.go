// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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
		if err := resolveCloudEnv(cloudFlags.EnvFile, cmd.Flags().Changed("env-file"), cloudFlags.Token, cloudFlags.URL); err != nil {
			return err
		}
		return runDaemon(modeCloud, flags, noTUI)
	},
}

// resolveCloudEnv loads the .env file (if present) and applies the --token /
// --url overrides into the process environment, then requires a cloud token.
// Shared by the `cloud` command and `demo --cloud`.
func resolveCloudEnv(envFile string, envFileExplicit bool, token, url string) error {
	if err := loadEnvFileInto(envFile, envFileExplicit); err != nil {
		return err
	}
	if token != "" {
		os.Setenv("RUNWISP_CLOUD_TOKEN", token)
	}
	if url != "" {
		os.Setenv("RUNWISP_CLOUD_URL", url)
	}
	if os.Getenv("RUNWISP_CLOUD_TOKEN") == "" {
		return fmt.Errorf("RUNWISP_CLOUD_TOKEN is required — set it via environment, .env file, or --token flag")
	}
	return nil
}

func init() {
	cloudCmd.Flags().StringVar(&cloudFlags.Token, "token", "", "cloud token (overrides RUNWISP_CLOUD_TOKEN)")
	cloudCmd.Flags().StringVar(&cloudFlags.URL, "url", "", "cloud API URL (overrides RUNWISP_CLOUD_URL)")
	cloudCmd.Flags().StringVar(&cloudFlags.EnvFile, "env-file", ".env", "path to .env file for cloud configuration")
	cloudCmd.Flags().BoolVar(&noTUI, "no-tui", false, "run in headless daemon mode (no interactive TUI)")
}

// loadEnvFileInto loads variables from the .env file into the process
// environment. If the file is missing and was explicitly requested, an error is
// returned; a missing default ".env" is silently ignored.
func loadEnvFileInto(envFile string, envFileExplicit bool) error {
	err := godotenv.Load(envFile)
	if err == nil {
		return nil
	}
	if os.IsNotExist(err) && !envFileExplicit {
		return nil
	}
	return fmt.Errorf("cannot load env file %q: %w", envFile, err)
}
