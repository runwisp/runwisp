// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/server/auth"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/sebest/xff"
	"log/slog"
)

// Server exposes the HTTP API and serves the UI.
type Server struct {
	router            *chi.Mux
	api               huma.API
	db                storage.RunRepository
	notifyRepo        storage.NotificationRepository
	notifyHub         NotificationHub
	taskManager       runtime.TaskRunner
	tasks             map[string]*model.Task
	scheduler         runtime.NextRunGetter
	host              string
	port              int
	logDir            string
	eventBus          events.EventBus
	auth              *auth.Service
	passwordEphemeral bool
	trustedProxies    *xff.Options
	logOutput         io.Writer
	daemonLogBuffer   *DaemonLogBuffer
	runService        *runService
	stats             *statsProvider
	metrics           *MetricsCollector
	streams           *streamLimiter
	httpServer        *http.Server
	unixServer        *http.Server
	metricsServer     *http.Server
	socketPath        string
	metricsEnabled    bool
	metricsListen     string
}

// DaemonInfo, TaskBrief, and CapInfo live in the model package.

type Options struct {
	DB                storage.RunRepository
	NotificationDB    storage.NotificationRepository // optional; nil disables /api/notifications
	NotificationHub   NotificationHub                // optional; nil disables /api/notifications/stream
	TaskManager       runtime.TaskRunner
	Tasks             map[string]*model.Task
	Scheduler         runtime.NextRunGetter
	Host              string // Bind address (default: 127.0.0.1)
	Port              int
	SocketPath        string // Unix socket path for local CLI/TUI; empty disables socket listener
	LogDir            string
	EventBus          events.EventBus
	Password          string            // Authentication password (required)
	PasswordEphemeral bool              // True when the daemon minted Password in memory at boot (no RUNWISP_PASSWORD)
	JWTSecret         string            // JWT signing secret (derived in-memory)
	DaemonInfo        *model.DaemonInfo // Static identity/config info for /api/info
	DaemonLogBuffer   *DaemonLogBuffer  // Ring buffer for daemon log streaming (optional)
	LogOutput         io.Writer         // Destination for HTTP request logs (default: os.Stderr)
	MetricsEnabled    bool              // When false, /metrics is not mounted anywhere
	MetricsListen     string            // When non-empty, bind /metrics on a separate listener (e.g. "127.0.0.1:9478")
}

func New(opts Options) (*Server, error) {
	if opts.Password == "" {
		return nil, errors.New("password must be provided to start the web server")
	}

	trustedProxies, err := parseTrustedProxies(os.Getenv("RUNWISP_TRUST_PROXY"))
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
		tasks:             opts.Tasks,
		scheduler:         opts.Scheduler,
		host:              opts.Host,
		port:              opts.Port,
		socketPath:        opts.SocketPath,
		logDir:            opts.LogDir,
		eventBus:          opts.EventBus,
		auth:              authSvc,
		passwordEphemeral: opts.PasswordEphemeral,
		trustedProxies:    trustedProxies,
		metricsEnabled:    opts.MetricsEnabled,
		metricsListen:     opts.MetricsListen,
	}

	s.runService = newRunService(opts.DB, opts.TaskManager, opts.Tasks, opts.Scheduler, opts.LogDir)
	s.stats = newStatsProvider(opts.DaemonInfo, time.Now())
	s.metrics = NewMetricsCollector(32) // ~2.5 min at 5s intervals
	s.metrics.Start(5 * time.Second)
	s.logOutput = opts.LogOutput
	if s.logOutput == nil {
		s.logOutput = os.Stderr
	}
	s.daemonLogBuffer = opts.DaemonLogBuffer
	s.streams = newStreamLimiter(maxConcurrentStreams, maxStreamsPerIP)
	if err := s.setupRoutes(); err != nil {
		return nil, fmt.Errorf("setup routes: %w", err)
	}
	return s, nil
}

func (srv *Server) Start() error {
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

	if err := srv.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	if err := <-unixErrCh; err != nil {
		return err
	}
	return <-metricsErrCh
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
		ln.Close()
		_ = os.Remove(srv.socketPath)
		return nil, fmt.Errorf("chmod socket: %w", err)
	}
	return &peercredListener{Listener: ln, selfUID: uint32(os.Getuid())}, nil
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
		return fmt.Errorf("%s is in use by another process", path)
	}
	return os.Remove(path)
}

// Shutdown gracefully stops the HTTP server and metrics collector.
func (srv *Server) Shutdown(ctx context.Context) error {
	srv.metrics.Stop()

	var wg sync.WaitGroup
	var tcpErr, unixErr, metricsErr error

	if srv.httpServer != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tcpErr = srv.httpServer.Shutdown(ctx)
		}()
	}
	if srv.unixServer != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unixErr = srv.unixServer.Shutdown(ctx)
		}()
	}
	if srv.metricsServer != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			metricsErr = srv.metricsServer.Shutdown(ctx)
		}()
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
