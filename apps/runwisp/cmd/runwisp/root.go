// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"log/slog"

	"github.com/runwisp/runwisp/internal/clilog"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/internal/version"
	"github.com/spf13/cobra"
)

// Flags consolidates persistent CLI flag values into a single struct so that
// individual subcommand files don't each declare their own globals.
type Flags struct {
	CfgFile string
	DataDir string
	Socket  string
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
	Use:   "runwisp",
	Short: "RunWisp — lightweight cron daemon with a web UI",
	Long: `RunWisp is a standalone cron daemon and process supervisor with a web UI and a terminal UI.

Tasks are defined in runwisp.toml (the sole source of truth). Machine-readable
surfaces for scripts and AI agents:
  runwisp schema           JSON Schema for runwisp.toml (also at ` + config.SchemaURL + `)
  runwisp validate --json  validate a config; errors carry key/line/column
  runwisp exec <t> --json  run a task and print its outcome as JSON
  runwisp status --json    daemon + task snapshot as JSON
  runwisp openapi          the REST API's OpenAPI 3.1 spec
Dense agent reference: https://docs.runwisp.com/agents/reference.md`,
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
		if err := resolvePortConfig(cmd); err != nil {
			return err
		}
		resolvePathDefaults(os.Geteuid())
		return datadir.EnsureDir(flags.DataDir)
	},
}

func init() {
	// Minimal bootstrap so anything logged before flags are parsed is sane;
	// PersistentPreRunE finalizes level/format from flags + env.
	clilog.Configure(clilog.Options{Level: slog.LevelInfo, Format: clilog.FormatAuto})

	// "" (not a literal default) so PersistentPreRunE can tell "flag left
	// alone" from "flag set to what the default would produce anyway" and
	// apply the env var / euid-derived default in its place. Cobra evaluates
	// flag defaults once in init(), before os.Geteuid() or an env read would
	// matter, so the real default is filled in there instead.
	rootCmd.PersistentFlags().StringVarP(&flags.CfgFile, "config", "c", "", "path to configuration file (default: ./runwisp.toml, or /etc/runwisp/runwisp.toml as root; env: RUNWISP_CONFIG)")
	rootCmd.PersistentFlags().StringVar(&flags.DataDir, "data", "", "directory for all persistent data (default: ./.runwisp, or /var/lib/runwisp as root; env: RUNWISP_DATA)")
	rootCmd.PersistentFlags().StringVar(&flags.Socket, "socket", os.Getenv("RUNWISP_SOCKET"), "control socket path; daemon binds it, CLI connects to it (default: <data>/runwisp.sock; env: RUNWISP_SOCKET)")
	rootCmd.PersistentFlags().IntVarP(&flags.Port, "port", "p", 9477, "HTTP server port (env: RUNWISP_PORT)")
	rootCmd.PersistentFlags().StringVar(&flags.Host, "host", firstNonEmpty(os.Getenv("RUNWISP_HOST"), "127.0.0.1"), "HTTP server bind address (use 0.0.0.0 to listen on all interfaces) (env: RUNWISP_HOST)")
	rootCmd.PersistentFlags().StringVar(&flags.logLevelRaw, "log-level", "", "log verbosity: debug, info, warn, error (env: RUNWISP_LOG_LEVEL)")
	rootCmd.PersistentFlags().StringVar(&flags.logFormatRaw, "log-format", "", "log format: auto, text, json (env: RUNWISP_LOG_FORMAT)")

	rootCmd.AddCommand(daemonCmd)
	rootCmd.AddCommand(cloudCmd)
	rootCmd.AddCommand(tuiCmd)
	rootCmd.AddCommand(validateCmd)
	rootCmd.AddCommand(importCmd)
	rootCmd.AddCommand(promoteCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(execCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(restartCmd)
	rootCmd.AddCommand(reloadCmd)
	rootCmd.AddCommand(passwordCmd)
	rootCmd.AddCommand(openapiCmd)
	rootCmd.AddCommand(schemaCmd)
	rootCmd.AddCommand(agentGuideCmd)
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

// resolvePortConfig applies the RUNWISP_PORT env fallback for --port. It only
// consults the env when the flag was left at its default (not explicitly
// passed on the command line), so an explicit --port always wins. A malformed
// RUNWISP_PORT is a startup error, not a silently ignored value.
func resolvePortConfig(cmd *cobra.Command) error {
	if cmd.Flags().Changed("port") {
		return nil
	}
	raw := strings.TrimSpace(os.Getenv("RUNWISP_PORT"))
	if raw == "" {
		return nil
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port <= 0 || port > 65535 {
		return &userFacingError{title: fmt.Sprintf("RUNWISP_PORT must be a valid port number (got %q)", raw)}
	}
	flags.Port = port
	return nil
}

// resolvePathDefaults fills flags.CfgFile / flags.DataDir when the operator
// left --config / --data unset, with precedence flag > env > euid-derived
// default. It must run before anything reads either field — daemon boot,
// EnsureDir, the socket path derived from DataDir, and the child-process args
// a spawned daemon is handed all depend on the result.
//
// The euid check exists for one reason: `sudo runwisp reload` against a
// system install has to find the same runwisp.toml / data dir the daemon
// itself defaulted to when it (also root) started with no flags. Without
// this, root's default was the cwd-relative "./runwisp.toml" / "./.runwisp"
// same as any other user, so a `sudo runwisp reload` run from anywhere but
// the exact directory the daemon happened to boot in silently talked to the
// wrong (or no) socket.
func resolvePathDefaults(euid int) {
	if flags.CfgFile == "" {
		flags.CfgFile = defaultConfigPath(euid, os.Getenv("RUNWISP_CONFIG"))
	}
	if flags.DataDir == "" {
		flags.DataDir = defaultDataDir(euid, os.Getenv("RUNWISP_DATA"))
	}
}

// defaultConfigPath resolves the --config default: an explicit RUNWISP_CONFIG
// wins over everything (including euid), then root gets /etc/runwisp/runwisp.toml,
// then everyone else keeps the pre-existing cwd-relative default. euid is a
// parameter rather than an inline os.Geteuid() call so a test can describe a
// machine other than the one running the test.
func defaultConfigPath(euid int, env string) string {
	if env != "" {
		return env
	}
	if euid == 0 {
		return "/etc/runwisp/runwisp.toml"
	}
	return "runwisp.toml"
}

// defaultDataDir is defaultConfigPath's counterpart for --data.
func defaultDataDir(euid int, env string) string {
	if env != "" {
		return env
	}
	if euid == 0 {
		return "/var/lib/runwisp"
	}
	return ".runwisp"
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
