// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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

	"github.com/mattn/go-isatty"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/clilog"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/cronprobe"
	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runlog"
	"github.com/runwisp/runwisp/internal/runtime"
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

// noTUI is the bind target for `runwisp cloud --no-tui`; cobra needs a stable
// address. The resolved value is read once at the cloud RunE boundary and
// passed into runDaemon as the headless argument — no logic helper reads it.
// (`runwisp daemon` passes headless=true directly; the bare `runwisp` passes
// false.)
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

func runDaemon(mode daemonMode, f Flags, headless bool) (err error) {
	headless, tuiAutoDisabled := resolveHeadless(headless)

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
	debugWriter := configureBootLogRouting(logBuffer, f, headless)
	if tuiAutoDisabled {
		slog.Info("no interactive terminal detected — running headless (pass --no-tui to silence this)")
	}
	// If boot fails before the bubbletea program attaches, slog points at the
	// TUI debug buffer which nobody will ever read. Reroute it back to stderr
	// so the operator always sees why the daemon died.
	if !headless {
		defer rerouteLogsToStderrOnError(&err)
	}

	// Refuse to boot if another daemon already owns this data dir. Two daemons
	// on one data dir clobber the shared PID file (orphaning the first) and open
	// the same SQLite database, so fail hard before touching any state.
	if err := ensureNoRunningDaemon(f); err != nil {
		return err
	}

	// Open database first — config values (fingerprint, jwt_secret) live there.
	db, err := storage.New(f.DBPath())
	if err != nil {
		return err
	}

	cfg, err := loadDaemonConfig(context.Background(), db, mode, f)
	if err != nil {
		_ = db.Close()
		return err
	}

	// Warn standalone users who have RUNWISP_CLOUD_TOKEN set but aren't using
	// the cloud subcommand.
	if mode == modeStandalone && os.Getenv("RUNWISP_CLOUD_TOKEN") != "" {
		slog.Warn("RUNWISP_CLOUD_TOKEN is set but ignored in standalone mode — use 'runwisp cloud' to start in cloud mode")
	}

	tlsCfg, err := resolveTLS(f, cfg.Config.Daemon)
	if err != nil {
		_ = db.Close()
		return err
	}

	logSecurityWarnings(cfg, f, tlsCfg)

	// Pin the on-disk identity of runwisp.toml + env_files. Reload is
	// restart-only; the snapshot lets /api/info report config_stale so every
	// surface can show a "restart to apply" hint instead of silently ignoring
	// edits.
	configSnap := config.NewSnapshot(f.CfgFile, cfg.Config, time.Now())

	svc, err := initDaemonServices(context.Background(), cfg, db, mode, f)
	if err != nil {
		return err
	}
	defer svc.DB.Close()

	if pidErr := datadir.WritePidFile(f.DataDir); pidErr != nil {
		return fmt.Errorf("write PID file: %w", pidErr)
	}
	defer datadir.CleanPidFile(f.DataDir)

	daemonInfo := buildDaemonInfo(cfg, svc, configSnap.LoadedAt(), f.Port)

	reconciler, reloadFn := newReconciler(mode, cfg, svc, f, configSnap)
	defer startCronHoldWatcher(reconciler, cfg.Config)()

	srv, err := server.New(server.Options{
		DB:                svc.DB,
		NotificationDB:    svc.DB,
		NotificationHub:   svc.Notify.serverHub(),
		TaskManager:       svc.TaskManager,
		Tasks:             svc.Tasks,
		Scheduler:         svc.Scheduler,
		Host:              f.Host,
		Port:              f.Port,
		DataDir:           absPathOrFallback(f.DataDir),
		ConfigPath:        absPathOrFallback(f.CfgFile),
		SocketPath:        localAPISocketPath(f),
		LogDir:            f.LogDir(),
		EventBus:          svc.EventBus,
		Password:          cfg.Password,
		PasswordEphemeral: cfg.PasswordEphemeral,
		JWTSecret:         cfg.JWTSecret,
		NoAuth:            cfg.NoAuth,
		TrustedProxies:    os.Getenv("RUNWISP_TRUSTED_PROXIES"),
		DaemonInfo:        daemonInfo,
		ConfigStale:       configSnap.Stale,
		ConfigWarnings:    configWarningsFn(reconciler, cfg.Config),
		DaemonLogBuffer:   logBuffer,
		MetricsEnabled:    cfg.Config.Daemon.MetricsEnabled,
		MetricsListen:     cfg.Config.Daemon.MetricsListen,
		TLSCert:           tlsCfg.CertPath,
		TLSKey:            tlsCfg.KeyPath,
		Reload:            reloadFn,
	})
	if err != nil {
		return err
	}

	startupInfo := uikit.StartupInfo{
		Version:    version.Version,
		ConfigPath: absPathOrFallback(f.CfgFile),
		DataDir:    absPathOrFallback(f.DataDir),
		DBPath:     f.DBPath(),
		LogDir:     f.LogDir(),
		Port:       f.Port,
		ListenURL:  daemonListenURL(cfg.Config, f),

		TLSFingerprint: tlsCfg.Fingerprint,

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
		Headless:     headless,

		ScheduleWarnings: svc.ScheduleResult.Warnings,
		InitWarnings:     svc.InitWarnings,
		CrashedRuns:      svc.CrashedRuns,
		PendingRuns:      svc.PendingSummary,
		CatchUpTriggered: svc.CatchUpResult.Triggered,
	}

	// Cloud client is only started in cloud mode; standalone gets no-ops.
	cancelCloud, cloudWG := startCloudIfEnabled(mode, cfg, svc, srv)
	defer cancelCloud()

	// fatalCh carries a fatal server-start error from the supervisor goroutine
	// back to the shutdown path, so a self-triggered teardown logs the real
	// cause instead of a phantom signal. Buffered + send-before-self-signal so
	// the value is visible by the time the signal handler reads it.
	fatalCh := make(chan error, 1)
	go superviseServerStart(srv, fatalCh)

	emitStartupBanner(startupInfo, f)

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
		fatalCh:     fatalCh,
		reconciler:  reconciler,
		debugWriter: debugWriter,
		logBuffer:   logBuffer,
		cancelCloud: cancelCloud,
		cloudWG:     cloudWG,
	}

	if headless {
		defer runlog.Subscribe(svc.EventBus)()
		emitReadiness(rt, startupInfo.ListenURL, f)
		return runHeadless(rt)
	}

	return runWithTUI(rt, startupInfo, f)
}

// rerouteLogsToStderrOnError sends CLI logging back to stderr when runDaemon is
// returning an error. Deferred with a pointer to runDaemon's named return so it
// observes the final error at unwind. Split out to keep runDaemon's cognitive
// complexity in check.
func rerouteLogsToStderrOnError(err *error) {
	if *err != nil {
		clilog.SetOutput(os.Stderr)
	}
}

// newReconciler wires the reload reconciler that powers `runwisp reload` /
// SIGHUP. Standalone only: cloud mode has no local scheduler to reconcile, so
// it returns (nil, nil), leaving POST /api/reload reporting "not available in
// this mode".
func newReconciler(mode daemonMode, cfg *daemonConfig, svc *daemonServices, f Flags, snap *config.Snapshot) (*runtime.Reconciler, func() (model.ReloadResult, error)) {
	if mode != modeStandalone {
		return nil, nil
	}
	r := runtime.NewReconciler(runtime.ReconcilerDeps{
		ConfigPath: f.CfgFile,
		Baseline:   cfg.Config,
		Registry:   svc.Tasks,
		Scheduler:  svc.Scheduler,
		Manager:    svc.TaskManager,
		DB:         svc.DB,
		Snapshot:   snap,
		Now:        time.Now,
	})
	return r, r.Reconcile
}

// startCronHoldWatcher starts the loop that keeps the cron holds honest, so an
// operator who retires cron gets their jobs back without running `runwisp
// reload` — and, in the other direction, a cron that comes back reclaims them
// before both schedulers fire the same job.
//
// Skipped unless this config actually reads a crontab: with no include_cron the
// probe could not change a single decision, and starting it anyway would exec
// systemctl every minute on every ordinary install. Also skipped in cloud mode,
// which has no local scheduler to refresh (nil reconciler).
//
// Always returns a non-nil stop func so the caller can defer it unconditionally.
func startCronHoldWatcher(r *runtime.Reconciler, cfg *config.Config) context.CancelFunc {
	state, readsCron := config.CronHold(cfg)
	if r == nil || !readsCron {
		return func() {}
	}
	return runtime.StartCronHoldWatcher(cronprobe.Probe, r.RefreshCronHolds, state)
}

// configWarningsFn returns the hook /api/info calls for the live config's
// non-fatal findings. It prefers the reconciler's baseline so a reload's warnings
// replace boot's; in a mode with no reconciler (cloud) the boot config is the live
// one and can't change.
func configWarningsFn(r *runtime.Reconciler, boot *config.Config) func() []string {
	if r != nil {
		return r.Warnings
	}
	return func() []string { return config.Warnings(boot) }
}

// isInteractiveTerminal reports whether both stdin (TUI key input) and stdout
// (TUI rendering) are attached to a real terminal. bubbletea needs both; when
// either is redirected, attaching the TUI would block forever on input that
// never arrives.
func isInteractiveTerminal() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
}

// resolveHeadless flips a TUI request (headless=false) to headless when stdin
// and stdout are not a real terminal. The interactive TUI needs both (bubbletea
// reads keys from stdin and renders to stdout); without them — a toolbox with no
// PTY, systemd, Docker, a process manager — attaching it blocks forever on input
// that never arrives, indistinguishable from a hang. `--no-tui` (and `runwisp
// daemon`) already pass headless=true. Returns the effective headless flag and
// whether the fallback fired, so the caller can log it once routing is set up.
func resolveHeadless(headless bool) (effective, autoDisabled bool) {
	if !headless && !isInteractiveTerminal() {
		return true, true
	}
	return headless, false
}

// installSignalHandler arms SIGINT/SIGTERM/SIGHUP on a buffered channel before
// any goroutine could self-signal (superviseServerStart raises SIGTERM on
// itself when srv.Start fails). SIGINT/SIGTERM shut the daemon down; SIGHUP
// triggers an explicit config reload (see runHeadless). The returned stop func
// reverses signal.Notify so subsequent processes inherit the default
// disposition. The buffer is sized for the rare SIGHUP-during-shutdown overlap.
func installSignalHandler() (<-chan os.Signal, func()) {
	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
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
func configureBootLogRouting(logBuffer *server.DaemonLogBuffer, f Flags, headless bool) *tui.DebugLogWriter {
	if headless {
		// Headless: slog → stderr (tee'd to the ring buffer for remote TUI
		// streaming) with daemon-mode timestamps and TTY-aware color gating.
		clilog.Configure(clilog.Options{
			Level:      f.LogLevel,
			Format:     f.LogFormat,
			Output:     io.MultiWriter(os.Stderr, clilog.NewPlainWriter(logBuffer)),
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
func startCloudIfEnabled(mode daemonMode, cfg *daemonConfig, svc *daemonServices, srv *server.Server) (context.CancelFunc, *sync.WaitGroup) {
	if mode == modeCloud {
		return startCloudClient(context.Background(), cfg, svc, srv)
	}
	return func() {}, &sync.WaitGroup{}
}

// superviseServerStart runs srv.Start in this goroutine and, on failure,
// reports the cause on fatalCh and then self-signals SIGTERM so the daemon's
// signal handler tears the rest of the process down cleanly instead of leaving
// us in a half-initialized state. The send happens before the signal so the
// handler can distinguish a fatal-error teardown from an external signal.
func superviseServerStart(srv *server.Server, fatalCh chan<- error) {
	if startErr := srv.Start(); startErr != nil {
		slog.Error("Server failed", "err", startErr)
		select {
		case fatalCh <- startErr:
		default:
		}
		p, _ := os.FindProcess(os.Getpid())
		_ = p.Signal(syscall.SIGTERM)
	}
}

// emitStartupBanner picks between the fancy multi-section TTY banner and the
// plain slog summary. The fancy banner is for interactive terminals; piped
// into Docker logs / journald (non-TTY) or under --log-format=json it would
// be escape-code noise, so headless emits the same essential facts as plain
// slog lines.
func emitStartupBanner(info uikit.StartupInfo, f Flags) {
	if clilog.FancyBanner(f.LogFormat) {
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
	// The TTY banner and the TUI header both surface the ephemeral password, so
	// only the headless path — every container, every systemd unit — could
	// otherwise come up with a login nobody can perform and say nothing about
	// it in the log.
	if info.PasswordEphemeral && !info.AuthDisabled {
		slog.Warn("no RUNWISP_PASSWORD set — generated a random password for this boot; the Web UI cannot be logged into until you set one, and every restart invalidates existing sessions",
			"hint", "runwisp password")
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
// [daemon] external_url when configured, else <scheme>://<bind-host>:<port>
// where scheme follows the resolved TLS mode. Wildcard binds are mapped to
// localhost because 0.0.0.0/:: are not themselves connectable addresses.
func daemonListenURL(cfg *config.Config, f Flags) string {
	if u := cfg.Daemon.ExternalURL; u != "" {
		return u
	}
	return localBindURL(tlsScheme(cfg.Daemon, f.Host), f.Host, f.Port)
}

// localBindURL builds the operator-reachable <scheme>://host:port for a bind,
// mapping wildcard binds to localhost (0.0.0.0/:: are not connectable
// addresses). It is the external_url-free fallback shared by daemonListenURL
// and the demo's --no-tui summary.
func localBindURL(scheme, host string, port int) string {
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "localhost"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, host, port)
}

// serverReadyTimeout bounds how long readiness probes wait for the listeners
// to bind before giving up and proceeding. Generous because a bind-mounted
// data dir can make the first socket bind slow; a genuine bind failure
// short-circuits via fatalCh long before this elapses.
const serverReadyTimeout = 10 * time.Second

// waitServerReady blocks until the server's listeners are bound, a fatal
// server-start error occurs, or timeout elapses. A consumed fatal error is put
// back on fatalCh so the shutdown path can still read it (readiness and
// shutdown run sequentially on the same goroutine, so the re-send never races).
func waitServerReady(rt *daemonRuntime, timeout time.Duration) {
	if rt.srv == nil {
		return
	}
	select {
	case <-rt.srv.Ready():
	case err := <-rt.fatalCh:
		rt.fatalCh <- err
	case <-time.After(timeout):
	}
}

// emitReadiness waits for the daemon's local API to answer, then logs an
// accurate readiness line. Headless operators (Docker / systemd / JSON
// pipelines) rely on this as the "daemon is up" signal in the absence of a
// banner cue. When the fancy TTY banner is shown it already prints
// "Listening on <url>" — emitting an extra slog line on top would duplicate
// the message and break the clean visual hand-off from banner → run events.
func emitReadiness(rt *daemonRuntime, listenURL string, f Flags) {
	// Wait for the listeners to bind before probing, so a slow (bind-mount)
	// boot doesn't trip a spurious "health check did not pass" warning. A fatal
	// bind error short-circuits the wait and flows through the shutdown path.
	waitServerReady(rt, serverReadyTimeout)
	client := apiclient.NewUnix(localAPISocketPath(f))
	if err := pollHealth(client, 3*time.Second); err != nil {
		slog.Warn("health check did not pass during startup", "err", err)
	}
	if clilog.FancyBanner(f.LogFormat) {
		return
	}
	if listenURL == "" {
		slog.Info("ready")
		return
	}
	slog.Info("ready, listening", "url", listenURL)
}

// logSecurityWarnings emits log warnings for security-sensitive configurations.
func logSecurityWarnings(cfg *daemonConfig, f Flags, tlsCfg tlsSetup) {
	if cfg.Config.Daemon.AllowCloudDispatch {
		slog.Warn("Cloud dispatch enabled — the cloud control plane can execute arbitrary commands (shell, container, compose) on this host")
	}
	nonLoopback := isNonLoopbackBind(f.Host)
	serving := tlsCfg.Scheme == "https"
	// Exactly one banner: when auth is disabled it subsumes the non-loopback
	// message, so the two warnings never stack. The cleartext-exposure banner
	// only fires when a non-loopback bind is still plain HTTP (tls = "off");
	// auto-HTTPS removes the eavesdrop risk, so it gets a calm fingerprint line
	// instead — the same SHA-256 the startup banner shows for verification.
	switch {
	case cfg.NoAuth:
		slog.Warn("Authentication is DISABLED (RUNWISP_NO_AUTH) — the API and Web UI accept unauthenticated requests")
		printNoAuthBanner(f.Host, nonLoopback)
	case nonLoopback && !serving:
		printNonLoopbackBanner(f.Host)
	}
	if serving {
		origin := "self-signed"
		if !tlsCfg.Generated {
			origin = "operator-provided"
		}
		slog.Info("Serving HTTPS", "bind", f.Host, "cert", origin, "fingerprint", "sha256:"+tlsCfg.Fingerprint)
	}
	for _, w := range config.Warnings(cfg.Config) {
		slog.Warn(w)
	}
	// Held jobs get a banner rather than a warning line: the operator has to see
	// that a set of their jobs is deliberately not being scheduled, and a single
	// slog.Warn among the boot INFO lines is exactly what the TUI's alt-screen
	// switch then wipes off the terminal.
	if held := config.Held(cfg.Config); held.Any() {
		slog.Warn("cron still owns some tasks; RunWisp is holding them",
			"count", len(held.Tasks), "cron", held.CronState)
		printHeldBanner(os.Stderr, held.Tasks, held.CronState)
	}
}

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
			"  (nginx, caddy, traefik) and set RUNWISP_TRUSTED_PROXIES=<proxy-CIDR> so the\n"+
			"  daemon honors X-Forwarded-Proto. Do not expose plaintext HTTP to the\n"+
			"  public internet.\n"+
			"================================================================================\n",
		host,
	)
	_, _ = fmt.Fprint(os.Stderr, banner)
}
