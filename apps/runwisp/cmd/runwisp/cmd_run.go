// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"io"
	"os"
	"sync"
	"syscall"

	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/runwisp/runwisp/internal/tui"
	"github.com/spf13/cobra"
	"log/slog"
)

// daemonMode controls which subsystems runDaemon initializes.
type daemonMode int

const (
	modeStandalone daemonMode = iota
	modeCloud
)

var noTUI bool

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start the daemon (default)",
	Long:  `Starts the scheduler, HTTP API, and web UI. Use --no-tui for headless/daemon mode.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runDaemon(modeStandalone)
	},
}

func init() {
	runCmd.Flags().BoolVar(&noTUI, "no-tui", false, "run in headless daemon mode (no interactive TUI)")
}

// demoTask is injected when no configuration file exists and cloud mode is disabled,
// so that `./runwisp` works out of the box with zero setup.
var demoTask = model.Task{
	Name:        "hello-world",
	Description: "Demo task — runs every minute. Create a runwisp.yaml to define your own tasks.",
	Run: `echo "👋 Hello from RunWisp!"
echo "Current time: $(date)"
echo ""
echo "This is a built-in demo task."
echo "Create a runwisp.yaml file to define your own tasks:"
echo "  runwisp init"
`,
	Trigger: model.TaskTrigger{
		Cron: "* * * * *",
	},
	Execution: model.TaskExecution{
		Concurrency: model.TaskConcurrency{
			Limit:  1,
			Policy: model.PolicySkip,
		},
	},
}

func runDaemon(mode daemonMode) error {
	// Set up log routing before opening the database so GORM logs go to the
	// right destination from the very first query.
	var debugWriter *tui.DebugLogWriter
	var logOutput io.Writer = os.Stderr
	if !noTUI {
		debugWriter = tui.NewDebugLogWriter()
		logOutput = debugWriter
	}

	// Ring buffer for streaming daemon logs to remote TUI clients.
	logBuffer := server.NewDaemonLogBuffer(1000)
	logOutput = io.MultiWriter(logOutput, logBuffer)

	// Open database first — config values (fingerprint, jwt_secret) live there.
	db, err := storage.New(flags.DBPath(), logOutput)
	if err != nil {
		return err
	}

	cfg, err := loadDaemonConfig(db, mode)
	if err != nil {
		_ = db.Close()
		return err
	}

	// Warn standalone users who have RUNWISP_CLOUD_TOKEN set but aren't using
	// the cloud subcommand.
	if mode == modeStandalone && os.Getenv("RUNWISP_CLOUD_TOKEN") != "" {
		slog.Warn("RUNWISP_CLOUD_TOKEN is set but ignored in standalone mode — use 'runwisp cloud' to start in cloud mode")
	}

	logSecurityWarnings(cfg)

	svc, err := initDaemonServices(cfg, db, mode)
	if err != nil {
		return err
	}
	defer svc.DB.Close()

	if pidErr := datadir.WritePidFile(flags.DataDir); pidErr != nil {
		slog.Warn("Failed to write PID file", "err", pidErr)
	}
	defer datadir.CleanPidFile(flags.DataDir)
	if cfg.PasswordGenerated {
		defer datadir.CleanPasswordFile(flags.DataDir)
	}

	daemonInfo := buildDaemonInfo(cfg, svc)

	var srv *server.Server
	if cfg.Password != "" {
		srv, err = server.New(server.Options{
			DB:              svc.DB,
			TaskManager:     svc.TaskManager,
			Tasks:           svc.TasksMap,
			Scheduler:       svc.Scheduler,
			Host:            flags.Host,
			Port:            flags.Port,
			LogDir:          flags.LogDir(),
			EventBus:        svc.EventBus,
			Password:        cfg.Password,
			JWTSecret:       cfg.JWTSecret,
			DaemonInfo:      daemonInfo,
			DaemonLogBuffer: logBuffer,
			LogOutput:       logOutput,
		})
		if err != nil {
			return err
		}
	}

	webUIDisabled := srv == nil && cfg.CloudConfig.Enabled

	startupInfo := tui.StartupInfo{
		Version:    server.Version,
		ConfigPath: flags.CfgFile,
		DataDir:    flags.DataDir,
		DBPath:     flags.DBPath(),
		LogDir:     flags.LogDir(),
		Port:       flags.Port,

		Fingerprint:  cfg.Fingerprint,
		UsingDemo:    cfg.UsingDemo,
		Capabilities: daemonInfo.Capabilities,
		Tasks:        daemonInfo.Tasks,

		PasswordGenerated: cfg.PasswordGenerated,
		Password:          cfg.Password,

		CloudEnabled:  cfg.CloudConfig.Enabled,
		WebUIDisabled: webUIDisabled,

		ScheduleWarnings: svc.ScheduleResult.Warnings,
		CrashedRuns:      svc.CrashedRuns,
		PendingRuns:      svc.PendingSummary,
		CatchUpTriggered: svc.CatchUpResult.Triggered,
	}

	// Cloud client is only started in cloud mode; standalone gets no-ops.
	var cancelCloud context.CancelFunc
	var cloudWG *sync.WaitGroup
	if mode == modeCloud {
		cancelCloud, cloudWG = startCloudClient(context.Background(), cfg, svc)
	} else {
		cancelCloud = func() {}
		cloudWG = &sync.WaitGroup{}
	}
	defer cancelCloud()

	go func() {
		if srv == nil {
			return
		}
		if startErr := srv.Start(); startErr != nil {
			slog.Error("Server failed", "err", startErr)
			p, _ := os.FindProcess(os.Getpid())
			_ = p.Signal(syscall.SIGTERM)
		}
	}()

	tui.PrintStartup(startupInfo)

	if noTUI {
		return runHeadless(cancelCloud, cloudWG, svc.Scheduler, svc.TaskManager)
	}

	return runWithTUI(srv, svc, startupInfo, debugWriter, cancelCloud, cloudWG)
}

// logSecurityWarnings emits log warnings for security-sensitive configurations.
func logSecurityWarnings(cfg *daemonConfig) {
	if cfg.Config.Daemon.CloudShellTasks {
		slog.Warn("Cloud shell dispatch enabled — the cloud control plane can execute arbitrary shell commands on this host")
	}
	if flags.Host != "127.0.0.1" && flags.Host != "::1" && flags.Host != "localhost" {
		slog.Warn("HTTP server bound to non-loopback address — the API and web UI are accessible from the network", "host", flags.Host)
	}
}
