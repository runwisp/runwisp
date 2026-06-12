// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"

	"log/slog"

	"github.com/runwisp/runwisp/internal/clilog"
	"github.com/runwisp/runwisp/internal/datadir"
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

	// logLevelRaw and logFormatRaw hold the raw CLI flag values; PersistentPreRunE
	// resolves them (with RUNWISP_LOG_LEVEL / RUNWISP_LOG_FORMAT env fallbacks)
	// into LogLevel / LogFormat for the daemon boot path to consume.
	logLevelRaw  string
	logFormatRaw string

	LogLevel  slog.Level
	LogFormat clilog.Format
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
		return runDefault(flags)
	},
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if err := resolveLogConfig(); err != nil {
			return err
		}
		return datadir.EnsureDir(flags.DataDir)
	},
}

func init() {
	// Minimal bootstrap so anything logged before flags are parsed is sane;
	// PersistentPreRunE finalizes level/format from flags + env.
	clilog.Configure(clilog.Options{Level: slog.LevelInfo, Format: clilog.FormatAuto})

	rootCmd.PersistentFlags().StringVarP(&flags.CfgFile, "config", "c", "runwisp.toml", "path to configuration file")
	rootCmd.PersistentFlags().StringVar(&flags.DataDir, "data", ".runwisp", "directory for all persistent data (db, logs, secrets)")
	rootCmd.PersistentFlags().IntVarP(&flags.Port, "port", "p", 9477, "HTTP server port")
	rootCmd.PersistentFlags().StringVar(&flags.Host, "host", "127.0.0.1", "HTTP server bind address (use 0.0.0.0 to listen on all interfaces)")
	rootCmd.PersistentFlags().StringVar(&flags.logLevelRaw, "log-level", "", "log verbosity: debug, info, warn, error (env: RUNWISP_LOG_LEVEL)")
	rootCmd.PersistentFlags().StringVar(&flags.logFormatRaw, "log-format", "", "log format: auto, text, json (env: RUNWISP_LOG_FORMAT)")

	rootCmd.AddCommand(daemonCmd)
	rootCmd.AddCommand(cloudCmd)
	rootCmd.AddCommand(tuiCmd)
	rootCmd.AddCommand(validateCmd)
	rootCmd.AddCommand(importCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(execCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(restartCmd)
	rootCmd.AddCommand(reloadCmd)
	rootCmd.AddCommand(passwordCmd)
	rootCmd.AddCommand(openapiCmd)
	rootCmd.AddCommand(serviceCmd)
	rootCmd.AddCommand(demoCmd)
}

// resolveLogConfig folds the --log-level/--log-format flags together with their
// RUNWISP_LOG_LEVEL / RUNWISP_LOG_FORMAT env fallbacks (flags win), validates
// them, and installs a clean (non-daemon) slog handler. The daemon boot path
// later reconfigures with daemon-mode timestamps, color gating, and the right
// output destination. Unknown values are rejected with a user-facing error
// rather than silently falling back.
func resolveLogConfig() error {
	level, err := clilog.ParseLevel(firstNonEmpty(flags.logLevelRaw, os.Getenv("RUNWISP_LOG_LEVEL")))
	if err != nil {
		return &userFacingError{title: err.Error()}
	}
	format, err := clilog.ParseFormat(firstNonEmpty(flags.logFormatRaw, os.Getenv("RUNWISP_LOG_FORMAT")))
	if err != nil {
		return &userFacingError{title: err.Error()}
	}

	flags.LogLevel = level
	flags.LogFormat = format
	clilog.Configure(clilog.Options{Level: level, Format: format})
	return nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
