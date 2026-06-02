// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/internal/demo"
	"github.com/runwisp/runwisp/internal/storage"
)

// envDemoTempDir names the env var that hands a spawned daemon the path of the
// throwaway demo directory it should delete on shutdown. Cleanup is bound to
// the daemon's lifecycle (see runDaemon), not the foreground TUI's, so the
// directory survives a "Keep Running" quit and is removed only once the daemon
// itself stops.
const envDemoTempDir = "RUNWISP_DEMO_TEMP"

var demoFlags struct {
	Cloud   bool
	Token   string
	URL     string
	EnvFile string
}

var demoCmd = &cobra.Command{
	Use:   "demo",
	Short: "Boot a throwaway, fully-populated RunWisp instance",
	Long: `Boots RunWisp against a temporary config and data directory so you can
explore the TUI and Web UI without writing a runwisp.toml.

The demo loads a realistic "Acme Notes" config (scheduled backups and health
checks, background services, manual deploy/import tasks) and — in standalone
mode — pre-seeds hundreds of historical runs with real captured logs, timed to
match each task's cron schedule. The daemon runs in the background just like
plain ` + "`runwisp`" + `; quit the TUI and choose "Shut Down" to stop it. Choose
"Keep Running" instead and the daemon stays up, reachable in your browser at the
bound port — but not via ` + "`runwisp tui`" + `, which looks in the default data
dir, not the demo's throwaway one. Everything lives under a temp directory that
is deleted when the daemon shuts down.

With --cloud the daemon connects to the control plane instead and no history is
seeded (the cloud owns it); RUNWISP_CLOUD_TOKEN is required, as for ` + "`runwisp cloud`" + `.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDemo(cmd)
	},
}

func init() {
	demoCmd.Flags().BoolVar(&demoFlags.Cloud, "cloud", false, "connect to the cloud control plane instead of seeding local history")
	demoCmd.Flags().StringVar(&demoFlags.Token, "token", "", "cloud token, with --cloud (overrides RUNWISP_CLOUD_TOKEN)")
	demoCmd.Flags().StringVar(&demoFlags.URL, "url", "", "cloud API URL, with --cloud (overrides RUNWISP_CLOUD_URL)")
	demoCmd.Flags().StringVar(&demoFlags.EnvFile, "env-file", ".env", "path to .env file, with --cloud")
}

// runDemo sets up a throwaway temp dir, writes the embedded demo config, seeds
// history (standalone only), spawns the background daemon against the temp dir,
// and attaches the TUI — mirroring the plain `runwisp` flow.
func runDemo(cmd *cobra.Command) error {
	tmp, err := os.MkdirTemp("", "runwisp-demo-")
	if err != nil {
		return fmt.Errorf("create demo temp dir: %w", err)
	}

	// Redirect every path the daemon and TUI resolve at the throwaway dir.
	flags.CfgFile = filepath.Join(tmp, "runwisp.toml")
	flags.DataDir = filepath.Join(tmp, "data")

	// Until the daemon takes ownership of the temp dir (via envDemoTempDir),
	// any early failure must clean it up here.
	if err := setupDemoDir(cmd); err != nil {
		os.RemoveAll(tmp)
		return err
	}

	// Fail fast on a port conflict (e.g. a real daemon already on this port)
	// before paying to spawn a background process that would fail to bind.
	if bindErr := probePortAvailable(flags.Host, flags.Port); bindErr != nil {
		os.RemoveAll(tmp)
		return portConflictError(flags.Host, flags.Port, bindErr)
	}

	// Hand the temp dir to the spawned daemon; from here it owns cleanup.
	os.Setenv(envDemoTempDir, tmp)

	if err := spawnDemoDaemon(); err != nil {
		os.RemoveAll(tmp)
		return err
	}

	client := apiclient.NewUnix(localAPISocketPath())
	logPath := filepath.Join(flags.DataDir, "daemon.log")
	if err := waitForDaemon(client, logPath, 15*time.Second); err != nil {
		// The daemon may have died before reaching its cleanup defer; make sure
		// the temp dir doesn't leak.
		_ = shutdownDaemon()
		os.RemoveAll(tmp)
		return err
	}

	return runTUIConnect(client)
}

// setupDemoDir writes the embedded config, creates the data/log dirs, and either
// resolves cloud credentials (--cloud) or seeds the fake run history.
func setupDemoDir(cmd *cobra.Command) error {
	if err := demo.WriteConfig(flags.CfgFile); err != nil {
		return err
	}
	if err := datadir.EnsureDir(flags.DataDir); err != nil {
		return err
	}
	if err := datadir.EnsureDir(flags.LogDir()); err != nil {
		return err
	}

	if demoFlags.Cloud {
		return resolveCloudEnv(demoFlags.EnvFile, cmd.Flags().Changed("env-file"), demoFlags.Token, demoFlags.URL)
	}
	return seedDemoHistory()
}

// seedDemoHistory loads the just-written demo config and populates the database
// and log directory with believable historical runs.
func seedDemoHistory() error {
	cfg, err := config.Load(flags.CfgFile)
	if err != nil {
		return fmt.Errorf("load demo config: %w", err)
	}
	db, err := storage.New(flags.DBPath())
	if err != nil {
		return err
	}
	defer db.Close()

	fmt.Fprintln(os.Stderr, "Seeding demo history (this can take a few seconds)...")
	n, err := demo.Seed(context.Background(), db, cfg, flags.LogDir(), time.Now())
	if err != nil {
		return fmt.Errorf("seed demo data: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Seeded %d demo runs.\n", n)
	return nil
}

// spawnDemoDaemon launches the background daemon against the temp dir. Cloud
// mode spawns the headless `cloud` subcommand; standalone reuses spawnDaemon.
func spawnDemoDaemon() error {
	if demoFlags.Cloud {
		return spawnDaemonProcess([]string{"cloud", "--no-tui",
			"--config", flags.CfgFile,
			"--data", flags.DataDir,
			"--port", strconv.Itoa(flags.Port),
		})
	}
	return spawnDaemon()
}
