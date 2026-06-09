// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/clilog"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runlog"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/runwisp/runwisp/internal/tui"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/version"
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
	// Arm SIGINT/SIGTERM before any goroutine could self-signal (superviseServerStart
	// raises SIGTERM on its own PID when srv.Start fails). Registered first so the
	// stop func runs last, after every other defer has unwound.
	sigCh, stopSignals := installSignalHandler()
	defer stopSignals()

	// `runwisp demo` boots this daemon against a throwaway temp dir (see
	// envDemoTempDir). Registered first ⇒ runs last, after the DB is closed and
	// the PID file is cleaned.
	if demoTmp := os.Getenv(envDemoTempDir); demoTmp != "" {
		defer os.RemoveAll(demoTmp)
	}

	// Ring buffer for streaming daemon logs to remote TUI clients.
	logBuffer := server.NewDaemonLogBuffer(1000)
	debugWriter := configureBootLogRouting(logBuffer)

	// Open database first — config values (fingerprint, jwt_secret) live there.
	db, err := storage.New(flags.DBPath())
	if err != nil {
		return err
	}

	cfg, err := loadDaemonConfig(context.Background(), db, mode)
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

	// Pin the on-disk identity of runwisp.toml + env_files. Reload is
	// restart-only; the snapshot lets /api/info report config_stale so every
	// surface can show a "restart to apply" hint instead of silently ignoring
	// edits.
	configSnap := config.NewSnapshot(flags.CfgFile, cfg.Config, time.Now())

	svc, err := initDaemonServices(context.Background(), cfg, db, mode)
	if err != nil {
		return err
	}
	defer svc.DB.Close()

	if pidErr := datadir.WritePidFile(flags.DataDir); pidErr != nil {
		slog.Warn("Failed to write PID file", "err", pidErr)
	}
	defer datadir.CleanPidFile(flags.DataDir)

	daemonInfo := buildDaemonInfo(cfg, svc, configSnap.LoadedAt())

	srv, err := server.New(server.Options{
		DB:                svc.DB,
		NotificationDB:    svc.DB,
		NotificationHub:   svc.Notify.serverHub(),
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
		NoAuth:            cfg.NoAuth,
		DaemonInfo:        daemonInfo,
		ConfigStale:       configSnap.Stale,
		DaemonLogBuffer:   logBuffer,
		MetricsEnabled:    cfg.Config.Daemon.MetricsEnabled,
		MetricsListen:     cfg.Config.Daemon.MetricsListen,
	})
	if err != nil {
		return err
	}

	startupInfo := uikit.StartupInfo{
		Version:    version.Version,
		ConfigPath: absPathOrFallback(flags.CfgFile),
		DataDir:    absPathOrFallback(flags.DataDir),
		DBPath:     flags.DBPath(),
		LogDir:     flags.LogDir(),
		Port:       flags.Port,
		ListenURL:  daemonListenURL(cfg.Config),

		Fingerprint:    cfg.Fingerprint,
		UsingDemo:      cfg.UsingDemo,
		Capabilities:   daemonInfo.Capabilities,
		Tasks:          daemonInfo.Tasks,
		Timezone:       daemonInfo.ResolvedTimezone,
		TimezoneSource: daemonInfo.TimezoneSource,
		ServiceManaged: daemonInfo.ServiceManaged,

		PasswordEphemeral: cfg.PasswordEphemeral,
		Password:          cfg.Password,
		AuthDisabled:      cfg.NoAuth,

		CloudEnabled: cfg.CloudConfig.Enabled,
		Headless:     noTUI,

		ScheduleWarnings: svc.ScheduleResult.Warnings,
		InitWarnings:     svc.InitWarnings,
		CrashedRuns:      svc.CrashedRuns,
		PendingRuns:      svc.PendingSummary,
		CatchUpTriggered: svc.CatchUpResult.Triggered,
	}

	// Cloud client is only started in cloud mode; standalone gets no-ops.
	cancelCloud, cloudWG := startCloudIfEnabled(mode, cfg, svc)
	defer cancelCloud()

	go superviseServerStart(srv)

	emitStartupBanner(startupInfo)

	// Headless: subscribe runlog AFTER the banner is on screen, then signal
	// readiness, then block on signals. Subscribing later matters because
	// catch-up / service-instance auto-starts during initDaemonServices fire
	// events asynchronously; an earlier subscribe would race the banner print
	// and intersperse `run started` lines through the middle of it. Their
	// existence is already visible in the banner ("Triggered N catch-up
	// runs", "service xN"). The TUI path narrates runs visually and so
	// skips this subscription.
	rt := &daemonRuntime{
		sigCh:       sigCh,
		svc:         svc,
		srv:         srv,
		debugWriter: debugWriter,
		logBuffer:   logBuffer,
		cancelCloud: cancelCloud,
		cloudWG:     cloudWG,
	}

	if noTUI {
		defer runlog.Subscribe(svc.EventBus)()
		emitReadiness(startupInfo.ListenURL)
		return runHeadless(rt)
	}

	return runWithTUI(rt, startupInfo)
}

// installSignalHandler arms SIGINT/SIGTERM on a buffered channel before any
// goroutine could self-signal (superviseServerStart raises SIGTERM on itself
// when srv.Start fails). The returned stop func reverses signal.Notify so
// subsequent processes inherit the default disposition.
func installSignalHandler() (<-chan os.Signal, func()) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	return sigCh, func() { signal.Stop(sigCh) }
}

// configureBootLogRouting wires the slog default to the right destination for
// the boot path: stderr (tee'd to logBuffer) when headless, or the TUI debug
// panel (also tee'd to logBuffer) when interactive. Returns the debug writer
// the TUI path needs to wire into the bubbletea program later; nil headless.
//
// Routing is set before the database opens so storage logs go to the right
// destination from the very first query. HTTP access logs flow through slog
// too (see internal/server/access_log.go), so the same destination is shared
// by every log line the daemon emits.
func configureBootLogRouting(logBuffer *server.DaemonLogBuffer) *tui.DebugLogWriter {
	if noTUI {
		// Headless: slog → stderr (tee'd to the ring buffer for remote TUI
		// streaming) with daemon-mode timestamps and TTY-aware color gating.
		clilog.Configure(clilog.Options{
			Level:      flags.LogLevel,
			Format:     flags.LogFormat,
			Output:     io.MultiWriter(os.Stderr, logBuffer),
			DaemonMode: true,
		})
		return nil
	}
	// TUI host: route slog into the debug panel + ring buffer right away, at
	// DEBUG, so every line (including HTTP access at DEBUG) is captured from
	// the first query — even before the bubbletea program attaches.
	// DebugLogWriter buffers up to 256 lines until SetProgram is called, then
	// flushes them. The fancy startup banner still prints to stderr via
	// tui.PrintStartup; this only redirects the slog stream.
	debugWriter := tui.NewDebugLogWriter()
	clilog.Configure(clilog.Options{
		Level:   slog.LevelDebug,
		Format:  clilog.FormatText,
		Output:  io.MultiWriter(debugWriter, logBuffer),
		TUIMode: true,
	})
	return debugWriter
}

// startCloudIfEnabled spins up the cloud client when running in cloud mode and
// returns the cancel/wait pair the shutdown path needs. Standalone gets a
// no-op cancel and empty WaitGroup so callers can defer/wait unconditionally.
func startCloudIfEnabled(mode daemonMode, cfg *daemonConfig, svc *daemonServices) (context.CancelFunc, *sync.WaitGroup) {
	if mode == modeCloud {
		return startCloudClient(context.Background(), cfg, svc)
	}
	return func() {}, &sync.WaitGroup{}
}

// superviseServerStart runs srv.Start in this goroutine and, on failure,
// self-signals SIGTERM so the daemon's signal handler tears the rest of the
// process down cleanly instead of leaving us in a half-initialized state.
func superviseServerStart(srv *server.Server) {
	if startErr := srv.Start(); startErr != nil {
		slog.Error("Server failed", "err", startErr)
		p, _ := os.FindProcess(os.Getpid())
		_ = p.Signal(syscall.SIGTERM)
	}
}

// emitStartupBanner picks between the fancy multi-section TTY banner and the
// plain slog summary. The fancy banner is for interactive terminals; piped
// into Docker logs / journald (non-TTY) or under --log-format=json it would
// be escape-code noise, so headless emits the same essential facts as plain
// slog lines.
func emitStartupBanner(info uikit.StartupInfo) {
	if clilog.FancyBanner(flags.LogFormat) {
		tui.PrintStartup(info)
		return
	}
	logStartupSummary(info)
}

// logStartupSummary emits the banner's essential facts as plain slog lines for
// non-TTY / JSON output, so a headless operator still sees version, paths, task
// count, and any startup warnings without ANSI escapes in the log stream. The
// listen URL is reported separately by emitReadiness once the daemon is up.
func logStartupSummary(info uikit.StartupInfo) {
	slog.Info("RunWisp starting",
		"version", info.Version,
		"config", info.ConfigPath,
		"data_dir", info.DataDir,
		"db", info.DBPath,
		"log_dir", info.LogDir,
		"tasks", len(info.Tasks),
	)
	if info.Fingerprint != "" {
		slog.Info("instance fingerprint", "fingerprint", info.Fingerprint)
	}
	if info.Timezone != "" {
		slog.Info("scheduler timezone", "timezone", info.Timezone, "source", info.TimezoneSource)
	}
	if info.UsingDemo {
		slog.Warn("no runwisp.toml found — running built-in demo task; create runwisp.toml to define your own tasks")
	}
	if info.CrashedRuns > 0 {
		slog.Warn("marked crashed runs from previous session", "count", info.CrashedRuns)
	}
	if pr := info.PendingRuns; pr.Total > 0 {
		slog.Info("recovered pending runs",
			"resumed", pr.Resumed, "requeued", pr.Queued,
			"skipped", pr.Skipped, "failed", pr.Failed)
	}
	if info.CatchUpTriggered > 0 {
		slog.Info("triggered catch-up runs for missed cron ticks", "count", info.CatchUpTriggered)
	}
	for _, w := range info.InitWarnings {
		slog.Warn("init warning", "detail", w)
	}
	for _, w := range info.ScheduleWarnings {
		slog.Warn("schedule warning", "detail", w)
	}
	if info.WebUIDisabled {
		slog.Info("web UI disabled (no password in cloud-only mode)")
	}
	// AuthDisabled needs no line here: logSecurityWarnings already emits the
	// WARN (and the stderr banner) in every mode before this summary runs.
}

// absPathOrFallback resolves p to an absolute path, falling back to the
// original value if resolution fails (e.g. unreadable CWD). Refusing to
// boot over a cosmetic resolution failure would be the wrong trade.
func absPathOrFallback(p string) string {
	if p == "" {
		return p
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// daemonListenURL returns the operator-reachable base URL of the Web UI:
// [daemon] external_url when configured, else http://<bind-host>:<port>.
// Wildcard binds are mapped to localhost because 0.0.0.0/:: are not themselves
// connectable addresses.
func daemonListenURL(cfg *config.Config) string {
	if u := cfg.Daemon.ExternalURL; u != "" {
		return u
	}
	host := flags.Host
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "localhost"
	}
	return fmt.Sprintf("http://%s:%d", host, flags.Port)
}

// emitReadiness waits for the daemon's local API to answer, then logs an
// accurate readiness line. Headless operators (Docker / systemd / JSON
// pipelines) rely on this as the "daemon is up" signal in the absence of a
// banner cue. When the fancy TTY banner is shown it already prints
// "Listening on <url>" — emitting an extra slog line on top would duplicate
// the message and break the clean visual hand-off from banner → run events.
func emitReadiness(listenURL string) {
	client := apiclient.NewUnix(localAPISocketPath())
	if err := pollHealth(client, 3*time.Second); err != nil {
		slog.Warn("health check did not pass during startup", "err", err)
	}
	if clilog.FancyBanner(flags.LogFormat) {
		return
	}
	if listenURL == "" {
		slog.Info("ready")
		return
	}
	slog.Info("ready, listening", "url", listenURL)
}

// logSecurityWarnings emits log warnings for security-sensitive configurations.
func logSecurityWarnings(cfg *daemonConfig) {
	if cfg.Config.Daemon.AllowCloudDispatch {
		slog.Warn("Cloud shell dispatch enabled — the cloud control plane can execute arbitrary shell commands on this host")
	}
	nonLoopback := flags.Host != "127.0.0.1" && flags.Host != "::1" && flags.Host != "localhost"
	// Exactly one banner: when auth is disabled it subsumes the non-loopback
	// message, so the two warnings never stack.
	if cfg.NoAuth {
		slog.Warn("Authentication is DISABLED (RUNWISP_NO_AUTH) — the API and Web UI accept unauthenticated requests")
		printNoAuthBanner(flags.Host, nonLoopback)
	} else if nonLoopback {
		printNonLoopbackBanner(flags.Host)
	}
	for _, w := range config.Warnings(cfg.Config) {
		slog.Warn(w)
	}
}

// printNonLoopbackBanner writes an unmissable stderr banner when the daemon
// is binding to an address reachable beyond localhost. Plain HTTP over a real
// network exposes the auth cookie and JWT in cleartext; we want operators to
// see this clearly without forcing them to add extra flags or terminate the
// process — they may know what they're doing (private LAN, behind a TLS proxy,
// etc.) and just need a visible reminder, not a roadblock.
// printNoAuthBanner writes an unmissable stderr banner when the daemon runs
// with RUNWISP_NO_AUTH. Like printNonLoopbackBanner it warns without blocking:
// the operator opted in explicitly and may be on a trusted setup (local dev,
// container on a private network) — they get a clear statement of what is
// exposed, not a roadblock. When the bind address is also non-loopback, this
// single banner carries both warnings so they never stack.
func printNoAuthBanner(host string, nonLoopback bool) {
	var b strings.Builder
	b.WriteString("\n================================================================================\n")
	b.WriteString("  SECURITY: Authentication is DISABLED (RUNWISP_NO_AUTH).\n")
	if nonLoopback {
		fmt.Fprintf(&b, "  The HTTP server is binding to %q (not loopback), so this applies to\n  anyone on the network.\n", host)
	}
	b.WriteString("  Whoever can reach this port has FULL control of the daemon: browsing run\n")
	b.WriteString("  history and logs, and triggering tasks that execute shell commands as this\n")
	b.WriteString("  user. Only use this for local development or on a trusted, isolated\n")
	b.WriteString("  network. Never expose this port to the public internet.\n")
	b.WriteString("================================================================================\n")
	_, _ = fmt.Fprint(os.Stderr, b.String())
}

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
