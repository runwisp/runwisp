// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/sebest/xff"
)

// Server exposes the HTTP API and serves the UI.
type Server struct {
	router          *chi.Mux
	api             huma.API
	db              storage.RunRepository
	taskManager     runtime.TaskRunner
	tasks           map[string]*model.Task
	scheduler       runtime.ScheduleSource
	host            string
	port            int
	logDir          string
	eventBus        events.EventBus
	auth            *AuthService
	trustedProxies  *xff.Options
	logOutput       io.Writer
	daemonLogBuffer *DaemonLogBuffer
	runService      *runService
	stats           *statsProvider
	metrics         *MetricsCollector
}

// DaemonInfo, TaskBrief, and CapInfo live in the model package.

type Options struct {
	DB              storage.RunRepository
	TaskManager     runtime.TaskRunner
	Tasks           map[string]*model.Task
	Scheduler       runtime.ScheduleSource
	Host            string // Bind address (default: 127.0.0.1)
	Port            int
	LogDir          string
	EventBus        events.EventBus
	Password        string            // Authentication password (required)
	JWTSecret       string            // JWT signing secret (persisted in DB)
	DaemonInfo      *model.DaemonInfo // Static identity/config info for /api/info
	DaemonLogBuffer *DaemonLogBuffer  // Ring buffer for daemon log streaming (optional)
	LogOutput       io.Writer         // Destination for HTTP request logs (default: os.Stderr)
}

func New(opts Options) (*Server, error) {
	if opts.Password == "" {
		return nil, errors.New("password must be provided to start the web server")
	}

	trustedProxies, err := parseTrustedProxies(os.Getenv("RUNWISP_TRUST_PROXY"))
	if err != nil {
		return nil, fmt.Errorf("parse trusted proxies: %w", err)
	}

	authSvc := NewAuthService(opts.Password, opts.JWTSecret)

	router := chi.NewRouter()

	s := &Server{
		router:         router,
		db:             opts.DB,
		taskManager:    opts.TaskManager,
		tasks:          opts.Tasks,
		scheduler:      opts.Scheduler,
		host:           opts.Host,
		port:           opts.Port,
		logDir:         opts.LogDir,
		eventBus:       opts.EventBus,
		auth:           authSvc,
		trustedProxies: trustedProxies,
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
	s.setupRoutes()
	return s, nil
}

func (srv *Server) Start() error {
	host := srv.host
	if host == "" {
		host = "127.0.0.1"
	}
	addr := fmt.Sprintf("%s:%d", host, srv.port)
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       120 * time.Second,
	}
	return httpServer.ListenAndServe()
}

func (srv *Server) API() huma.API {
	return srv.api
}
