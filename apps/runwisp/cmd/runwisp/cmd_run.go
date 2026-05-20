// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/runwisp/runwisp/internal/tui"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/version"
	"log/slog"
)

// daemonMode controls which subsystems runDaemon initializes.
type daemonMode int

const (
	modeStandalone daemonMode = iota
	modeCloud
)

// noTUI controls whether runDaemon attaches the interactive TUI.
// `runwisp daemon` flips it to true; `runwisp cloud` exposes it as a flag.
var noTUI bool

// demoTask is kept available for cloud-managed and other explicit branches
// that need a built-in task when no configuration file is present. The
// standalone daemon path errors out instead — missing config is a setup bug
// the operator must see.
var demoTask = model.Task{
	Name:        "hello-world",
	Description: "Demo task — runs every minute. Create a runwisp.toml to define your own tasks.",
	Run: `echo "👋 Hello from RunWisp!"
echo "Current time: $(date)"
echo ""
echo "This is a built-in demo task."
echo "Create a runwisp.toml to define your own tasks — see https://docs.runwisp.com/configuration/overview/"
`,
	Cron:          "* * * * *",
	Timezone:      "UTC",
	APITrigger:    true,
	MaxConcurrent: 1,
	OnOverlap:     model.PolicySkip,
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

	daemonInfo := buildDaemonInfo(cfg, svc)

	srv, err := server.New(server.Options{
		DB:                svc.DB,
		NotificationDB:    svc.DB,
		NotificationHub:   svc.Notify.Hub,
		TaskManager:       svc.TaskManager,
		Tasks:             svc.TasksMap,
		Scheduler:         svc.Scheduler,
		Host:              flags.Host,
		Port:              flags.Port,
		SocketPath:        datadir.SocketPath(flags.DataDir),
		LogDir:            flags.LogDir(),
		EventBus:          svc.EventBus,
		Password:          cfg.Password,
		PasswordEphemeral: cfg.PasswordEphemeral,
		JWTSecret:         cfg.JWTSecret,
		DaemonInfo:        daemonInfo,
		DaemonLogBuffer:   logBuffer,
		LogOutput:         logOutput,
		MetricsEnabled:    cfg.Config.Daemon.MetricsEnabled,
		MetricsListen:     cfg.Config.Daemon.MetricsListen,
	})
	if err != nil {
		return err
	}

	startupInfo := uikit.StartupInfo{
		Version:    version.Version,
		ConfigPath: flags.CfgFile,
		DataDir:    flags.DataDir,
		DBPath:     flags.DBPath(),
		LogDir:     flags.LogDir(),
		Port:       flags.Port,

		Fingerprint:    cfg.Fingerprint,
		UsingDemo:      cfg.UsingDemo,
		Capabilities:   daemonInfo.Capabilities,
		Tasks:          daemonInfo.Tasks,
		Timezone:       daemonInfo.ResolvedTimezone,
		TimezoneSource: daemonInfo.TimezoneSource,

		PasswordEphemeral: cfg.PasswordEphemeral,
		Password:          cfg.Password,

		CloudEnabled: cfg.CloudConfig.Enabled,

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
		if startErr := srv.Start(); startErr != nil {
			slog.Error("Server failed", "err", startErr)
			p, _ := os.FindProcess(os.Getpid())
			_ = p.Signal(syscall.SIGTERM)
		}
	}()

	tui.PrintStartup(startupInfo)

	if noTUI {
		return runHeadless(cancelCloud, cloudWG, svc, srv)
	}

	return runWithTUI(srv, svc, startupInfo, debugWriter, cancelCloud, cloudWG)
}

// logSecurityWarnings emits log warnings for security-sensitive configurations.
func logSecurityWarnings(cfg *daemonConfig) {
	if cfg.Config.Daemon.AllowCloudDispatch {
		slog.Warn("Cloud shell dispatch enabled — the cloud control plane can execute arbitrary shell commands on this host")
	}
	if flags.Host != "127.0.0.1" && flags.Host != "::1" && flags.Host != "localhost" {
		printNonLoopbackBanner(flags.Host)
	}
	for _, w := range config.GracefulStopWarnings(cfg.Config) {
		slog.Warn(w)
	}
}

// printNonLoopbackBanner writes an unmissable stderr banner when the daemon
// is binding to an address reachable beyond localhost. Plain HTTP over a real
// network exposes the auth cookie and JWT in cleartext; we want operators to
// see this clearly without forcing them to add extra flags or terminate the
// process — they may know what they're doing (private LAN, behind a TLS proxy,
// etc.) and just need a visible reminder, not a roadblock.
func printNonLoopbackBanner(host string) {
	banner := fmt.Sprintf(
		"\n"+
			"================================================================================\n"+
			"  SECURITY: HTTP server is binding to %q (not loopback).\n"+
			"  The web UI and API will be reachable from the network in cleartext HTTP.\n"+
			"  Auth tokens travel over the wire — terminate TLS at a reverse proxy\n"+
			"  (nginx, caddy, traefik) and set RUNWISP_TRUST_PROXY=<proxy-CIDR> so the\n"+
			"  daemon honors X-Forwarded-Proto. Do not expose plaintext HTTP to the\n"+
			"  public internet.\n"+
			"================================================================================\n",
		host,
	)
	_, _ = fmt.Fprint(os.Stderr, banner)
}
