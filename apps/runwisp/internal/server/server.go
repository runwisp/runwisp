// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"syscall"
	"time"

	"log/slog"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/server/auth"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/sebest/xff"
)

// Server exposes the HTTP API and serves the UI.
type Server struct {
	router            *chi.Mux
	api               huma.API
	db                storage.RunRepository
	notifyRepo        storage.NotificationRepository
	notifyHub         NotificationHub
	taskManager       runtime.TaskRunner
	scheduler         runtime.NextRunGetter
	host              string
	port              int
	dataDir           string
	configPath        string
	logDir            string
	eventBus          events.EventBus
	auth              *auth.Service
	passwordEphemeral bool
	noAuth            bool
	trustedProxies    *xff.Options
	daemonLogBuffer   *DaemonLogBuffer
	runService        *runService
	stats             *statsProvider
	configStale       func() bool
	metrics           *MetricsCollector
	streams           *streamLimiter
	httpServer        *http.Server
	unixServer        *http.Server
	metricsServer     *http.Server
	socketPath        string
	// ready is closed once every listener (TCP + Unix [+ metrics]) is bound and
	// about to serve, so readiness probes don't race the bind on a slow boot.
	ready          chan struct{}
	readyOnce      sync.Once
	metricsEnabled bool
	metricsListen  string
	// reload re-reads runwisp.toml and reconciles the live task set. nil
	// outside standalone mode (cloud mode has no local scheduler to reconcile),
	// in which case POST /api/reload reports the operation is unavailable.
	reload func() (model.ReloadResult, error)
}

// DaemonInfo, TaskBrief, and CapInfo live in the model package.

type Options struct {
	DB                storage.RunRepository
	NotificationDB    storage.NotificationRepository // optional; nil disables /api/notifications
	NotificationHub   NotificationHub                // optional; nil disables /api/notifications/stream
	TaskManager       runtime.TaskRunner
	Tasks             *runtime.TaskRegistry
	Scheduler         runtime.NextRunGetter
	Host              string // Bind address (default: 127.0.0.1)
	Port              int
	DataDir           string // Resolved data directory; disclosed via GET /api/instance (local only)
	ConfigPath        string // Resolved runwisp.toml path; disclosed via GET /api/instance (local only)
	SocketPath        string // Unix socket path for local CLI/TUI; empty disables socket listener
	LogDir            string
	EventBus          events.EventBus
	Password          string                             // Authentication password (required even with NoAuth — keeps the cookie/launch-ticket machinery alive)
	PasswordEphemeral bool                               // True when the daemon minted Password in memory at boot (no RUNWISP_PASSWORD)
	JWTSecret         string                             // JWT signing secret (derived in-memory)
	NoAuth            bool                               // RUNWISP_NO_AUTH: serve all /api/* routes over TCP without JWT/CHAP
	TrustedProxies    string                             // RUNWISP_TRUST_PROXY value (comma-separated CIDRs/IPs); read by the caller, parsed here
	DaemonInfo        *model.DaemonInfo                  // Static identity/config info for /api/info
	ConfigStale       func() bool                        // Per-request staleness probe for /api/info (optional; nil reports never-stale)
	DaemonLogBuffer   *DaemonLogBuffer                   // Ring buffer for daemon log streaming (optional)
	MetricsEnabled    bool                               // When false, /metrics is not mounted anywhere
	MetricsListen     string                             // When non-empty, bind /metrics on a separate listener (e.g. "127.0.0.1:9478")
	Reload            func() (model.ReloadResult, error) // Reconciles the live task set against runwisp.toml; nil disables POST /api/reload
}

func New(opts Options) (*Server, error) {
	if opts.Password == "" {
		return nil, errors.New("password must be provided to start the web server")
	}

	trustedProxies, err := parseTrustedProxies(opts.TrustedProxies)
	if err != nil {
		return nil, fmt.Errorf("parse trusted proxies: %w", err)
	}

	authSvc, err := auth.NewService(opts.Password, opts.JWTSecret, func(r *http.Request) bool {
		return isFromTrustedProxy(r, trustedProxies)
	})
	if err != nil {
		return nil, fmt.Errorf("init auth service: %w", err)
	}

	router := chi.NewRouter()

	s := &Server{
		router:            router,
		db:                opts.DB,
		notifyRepo:        opts.NotificationDB,
		notifyHub:         opts.NotificationHub,
		taskManager:       opts.TaskManager,
		scheduler:         opts.Scheduler,
		host:              opts.Host,
		port:              opts.Port,
		dataDir:           opts.DataDir,
		configPath:        opts.ConfigPath,
		socketPath:        opts.SocketPath,
		logDir:            opts.LogDir,
		eventBus:          opts.EventBus,
		auth:              authSvc,
		passwordEphemeral: opts.PasswordEphemeral,
		noAuth:            opts.NoAuth,
		trustedProxies:    trustedProxies,
		metricsEnabled:    opts.MetricsEnabled,
		metricsListen:     opts.MetricsListen,
		reload:            opts.Reload,
		ready:             make(chan struct{}),
	}

	s.runService = newRunService(opts.DB, opts.TaskManager, opts.Tasks, opts.Scheduler, opts.LogDir, opts.EventBus)
	s.stats = newStatsProvider(opts.DaemonInfo, time.Now())
	s.configStale = opts.ConfigStale
	s.metrics = NewMetricsCollector(32) // ~2.5 min at 5s intervals; sampling starts in Start()
	s.daemonLogBuffer = opts.DaemonLogBuffer
	s.streams = newStreamLimiter(maxConcurrentStreams, maxStreamsPerIP)
	if err := s.setupRoutes(); err != nil {
		return nil, fmt.Errorf("setup routes: %w", err)
	}
	return s, nil
}

func (srv *Server) Start() error {
	// Begin sampling here rather than in New so construction stays pure — a
	// Server built for a unit test that never calls Start spawns no goroutine.
	srv.metrics.Start(5 * time.Second)

	host := srv.host
	if host == "" {
		host = "127.0.0.1"
	}
	srv.httpServer = newHTTPServer(srv.router, fmt.Sprintf("%s:%d", host, srv.port))

	unixErrCh, err := srv.startUnixListener()
	if err != nil {
		return err
	}
	metricsErrCh, err := srv.startMetricsListener()
	if err != nil {
		return err
	}

	// Bind the TCP listener explicitly (rather than ListenAndServe) so we can
	// signal readiness only once every listener is actually bound — the Unix
	// socket above is already serving by now. A bind failure returns here and
	// flows to the caller; readiness is never signalled, so probes fall through
	// to the fatal path instead of waiting out their timeout.
	tcpLn, err := net.Listen("tcp", srv.httpServer.Addr)
	if err != nil {
		return fmt.Errorf("listen tcp %s: %w", srv.httpServer.Addr, err)
	}
	srv.signalReady()

	if err := srv.httpServer.Serve(tcpLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	if err := <-unixErrCh; err != nil {
		return err
	}
	return <-metricsErrCh
}

// Ready returns a channel that is closed once all listeners are bound and the
// server is about to serve. It never sends, only closes — safe to select on
// from multiple goroutines. On a bind failure it stays open (the caller's
// Start returns the error instead).
func (srv *Server) Ready() <-chan struct{} {
	return srv.ready
}

// signalReady closes the readiness channel exactly once.
func (srv *Server) signalReady() {
	srv.readyOnce.Do(func() { close(srv.ready) })
}

// newHTTPServer builds the primary HTTP server with the timeouts and header
// limits that all in-process http.Servers share.
func newHTTPServer(handler http.Handler, addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 16, // 64 KiB
	}
}

// startUnixListener binds the Unix socket and serves the chi mux on it,
// tagging every accepted conn as local-trusted via ConnContext so
// authOrLocalTrusted can short-circuit.
func (srv *Server) startUnixListener() (<-chan error, error) {
	unixLn, err := srv.openUnixListener()
	if err != nil {
		return nil, err
	}
	srv.unixServer = newHTTPServer(srv.router, "")
	srv.unixServer.ConnContext = func(ctx context.Context, _ net.Conn) context.Context {
		return context.WithValue(ctx, localTrustedKey{}, true)
	}
	return serveAsync(srv.unixServer, unixLn), nil
}

// startMetricsListener optionally binds a dedicated /metrics listener. The
// returned channel always delivers exactly one value — nil when metrics are
// disabled or the listener shuts down cleanly — so Start() can drain it
// unconditionally. Binding is synchronous so port-in-use or permission errors
// surface before httpServer starts blocking.
func (srv *Server) startMetricsListener() (<-chan error, error) {
	if !srv.metricsEnabled || srv.metricsListen == "" {
		return closedNilErrCh(), nil
	}
	metricsLn, err := net.Listen("tcp", srv.metricsListen)
	if err != nil {
		return nil, fmt.Errorf("listen metrics %s: %w", srv.metricsListen, err)
	}
	metricsMux := http.NewServeMux()
	metricsMux.HandleFunc("/metrics", srv.handleOpenMetrics)
	srv.metricsServer = newHTTPServer(metricsMux, "")
	return serveAsync(srv.metricsServer, metricsLn), nil
}

// serveAsync runs server.Serve(ln) in a goroutine and returns a buffered
// channel that receives the serve error, or nil for the expected
// ErrServerClosed graceful-shutdown signal.
func serveAsync(server *http.Server, ln net.Listener) <-chan error {
	errCh := make(chan error, 1)
	go func() {
		err := server.Serve(ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	return errCh
}

// closedNilErrCh returns a buffered channel pre-loaded with a nil error so
// callers can drain it without a nil-channel guard.
func closedNilErrCh() <-chan error {
	ch := make(chan error, 1)
	ch <- nil
	return ch
}

// openUnixListener binds a Unix domain socket at srv.socketPath. A stale socket
// file from a previous (crashed) daemon is removed if no peer answers it. The
// socket is chmod'd to 0600 immediately after binding; PEERCRED is then the
// belt to that suspender.
func (srv *Server) openUnixListener() (net.Listener, error) {
	if srv.socketPath == "" {
		return nil, errors.New("socket path required")
	}

	if err := removeStaleSocket(srv.socketPath); err != nil {
		return nil, fmt.Errorf("remove stale socket %s: %w", srv.socketPath, err)
	}

	ln, err := net.Listen("unix", srv.socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen unix %s: %w", srv.socketPath, err)
	}
	if err := os.Chmod(srv.socketPath, 0600); err != nil {
		// Some bind-mounted filesystems (Docker Desktop's osxfs/virtiofs, some
		// network FS) let us bind the socket but reject chmod on it with
		// EINVAL/ENOTSUP. The chmod is belt-and-suspenders — the socket already
		// lives in a 0700 data dir and every accept verifies peer UID via
		// PEERCRED — so downgrade those to a warning and keep serving. Other
		// errors (e.g. EPERM) still mean something is wrong and stay fatal.
		if isUnsupportedChmod(err) {
			slog.Warn("could not chmod control socket; relying on data-dir perms + PEERCRED",
				"path", srv.socketPath, "err", err)
		} else {
			ln.Close()
			_ = os.Remove(srv.socketPath)
			return nil, fmt.Errorf("chmod socket: %w", err)
		}
	}
	return &peercredListener{Listener: ln, selfUID: uint32(os.Getuid())}, nil
}

// isUnsupportedChmod reports whether a chmod error means the filesystem simply
// doesn't support chmod-ing this socket (rather than a real permission fault),
// which is safe to tolerate. ENOTSUP and EOPNOTSUPP share a value on Linux but
// are distinct on some BSDs, so both are checked.
func isUnsupportedChmod(err error) bool {
	return errors.Is(err, syscall.EINVAL) ||
		errors.Is(err, syscall.ENOTSUP) ||
		errors.Is(err, syscall.EOPNOTSUPP)
}

// removeStaleSocket unlinks an existing socket file iff no peer accepts on
// it. A live peer means another daemon owns the data dir; the caller will see
// EADDRINUSE on the subsequent net.Listen, which is the right error.
func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	// Refuse to follow a symlink or operate on a non-socket file — both are
	// signs of misuse (or attack) and removing them blindly would be unsafe.
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink; refusing to remove", path)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("%s exists and is not a socket; refusing to remove", path)
	}
	// Probe: if Dial succeeds, another live daemon owns this socket.
	conn, dialErr := net.DialTimeout("unix", path, 100*time.Millisecond)
	if dialErr == nil {
		conn.Close()
		return fmt.Errorf("another process is already listening on %s — a RunWisp daemon may already be running on this data dir; stop it or use a different --data/--socket", path)
	}
	return os.Remove(path)
}

// Shutdown gracefully stops the HTTP server and metrics collector.
func (srv *Server) Shutdown(ctx context.Context) error {
	if srv.metrics != nil {
		srv.metrics.Stop()
	}

	var wg sync.WaitGroup
	var tcpErr, unixErr, metricsErr error

	if srv.httpServer != nil {
		wg.Go(func() { tcpErr = srv.httpServer.Shutdown(ctx) })
	}
	if srv.unixServer != nil {
		wg.Go(func() { unixErr = srv.unixServer.Shutdown(ctx) })
	}
	if srv.metricsServer != nil {
		wg.Go(func() { metricsErr = srv.metricsServer.Shutdown(ctx) })
	}
	wg.Wait()

	if srv.socketPath != "" {
		if err := os.Remove(srv.socketPath); err != nil && !os.IsNotExist(err) {
			slog.Warn("Failed to remove socket file on shutdown", "path", srv.socketPath, "err", err)
		}
	}

	if tcpErr != nil {
		return tcpErr
	}
	if unixErr != nil {
		return unixErr
	}
	return metricsErr
}

func (srv *Server) API() huma.API {
	return srv.api
}
