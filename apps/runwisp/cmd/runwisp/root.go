// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"path/filepath"

	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/internal/tui"
	"github.com/runwisp/runwisp/internal/version"
	"github.com/spf13/cobra"
)

// Flags consolidates persistent CLI flag values into a single struct so that
// individual subcommand files don't each declare their own globals.
type Flags struct {
	CfgFile string
	DataDir string
	Host    string
	Port    int
}

func (f Flags) DBPath() string {
	return filepath.Join(f.DataDir, "runwisp.db")
}

func (f Flags) LogDir() string {
	return filepath.Join(f.DataDir, "logs")
}

var flags Flags

var rootCmd = &cobra.Command{
	Use:     "runwisp",
	Short:   "RunWisp — lightweight cron daemon with a web UI",
	Long:    `RunWisp is a standalone cron daemon and process supervisor with a web UI and a terminal UI.`,
	Version: version.Version,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDefault()
	},
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return datadir.EnsureDir(flags.DataDir)
	},
}

func init() {
	tui.ConfigureLogger()

	rootCmd.PersistentFlags().StringVarP(&flags.CfgFile, "config", "c", "runwisp.toml", "path to configuration file")
	rootCmd.PersistentFlags().StringVar(&flags.DataDir, "data", ".runwisp", "directory for all persistent data (db, logs, secrets)")
	rootCmd.PersistentFlags().IntVarP(&flags.Port, "port", "p", 9477, "HTTP server port")
	rootCmd.PersistentFlags().StringVar(&flags.Host, "host", "127.0.0.1", "HTTP server bind address (use 0.0.0.0 to listen on all interfaces)")

	rootCmd.AddCommand(daemonCmd)
	rootCmd.AddCommand(cloudCmd)
	rootCmd.AddCommand(tuiCmd)
	rootCmd.AddCommand(validateCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(execCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(passwordCmd)
	rootCmd.AddCommand(openapiCmd)
	rootCmd.AddCommand(serviceCmd)
}
