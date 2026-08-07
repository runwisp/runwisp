// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"log/slog"

	"github.com/coder/websocket"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/model"
)

const (
	authReadTimeout         = 20 * time.Second
	heartbeatInterval       = 5 * time.Second
	heartbeatSilenceTimeout = 15 * time.Second
	watchdogInterval        = 2 * time.Second
	writeTimeout            = 5 * time.Second
	// serviceStatusResendInterval re-pushes every service's supervisor snapshot on
	// this cadence so the control plane's view survives its snapshot TTL (90s) for
	// a stable service that produces no lifecycle events. Kept well under that TTL.
	serviceStatusResendInterval = 30 * time.Second
	outboundMessageBufferSize   = 256
	maxPendingExecutionUpdates  = 2048
)

const maxInboundMessageSize int64 = 4 * 1024 * 1024 // 4 MiB

type Dependencies struct {
	TaskManager       TaskRunner
	RunRepo           ExternalRunGetter
	PendingUploadRepo PendingLogUploadRepository
	EventBus          EventSubscriber
	LocalTasks        map[string]*model.Task
	LogDir            string
	Availability      executor.Availability
	OnConnected       func()
	// RequestRestart asks the daemon to restart its own process when cloud
	// sends agent:restart. nil rejects the command (e.g. not service-managed).
	RequestRestart func() error
	// SystemStats, when set, returns a live host snapshot the heartbeat
	// piggybacks for the control plane's per-runner metrics view. nil sends a
	// plain ping (the cloud falls back to identity-only runner info).
	SystemStats func() model.SystemStats
	// Now is the wall-clock source used by sub-components that persist
	// timestamps (currently only the log uploader). Production wires
	// time.Now; tests inject a fixed clock for deterministic fixtures. nil
	// falls back to time.Now.
	Now func() time.Time
}

type Client struct {
	config  Config
	runRepo ExternalRunGetter
	logDir  string

	syncClient   *TaskSyncClient
	taskManager  TaskRunner
	localTasks   map[string]*model.Task
	availability executor.Availability
	onConnected  func()
	handler      *InboundHandler
	tracker      *ExecutionTracker
	uploader     *LogUploader
	bridge       *EventBridge
	sessions     *sessionRunner

	conn *connectionManager
}

type wsSession struct {
	conn         *websocket.Conn
	outbound     chan []byte
	lastReceived atomic.Int64
}

func NewClient(cfg Config, deps Dependencies) (*Client, error) {
	if !cfg.Enabled {
		return nil, nil
	}
	if deps.TaskManager == nil {
		return nil, fmt.Errorf("cloud client requires task manager")
	}
	if deps.RunRepo == nil {
		return nil, fmt.Errorf("cloud client requires run repository")
	}
	if deps.EventBus == nil {
		return nil, fmt.Errorf("cloud client requires event bus")
	}

	localTasks := make(map[string]*model.Task, len(deps.LocalTasks))
	for name, task := range deps.LocalTasks {
		if task == nil {
			continue
		}
		copyTask := *task
		localTasks[name] = &copyTask
	}

	tracker := NewExecutionTracker()
	connMgr := newConnectionManager(tracker)

	uploader := NewLogUploader(deps.PendingUploadRepo, deps.RunRepo, deps.LogDir, deps.Now)

	client := &Client{
		config:       cfg,
		runRepo:      deps.RunRepo,
		logDir:       deps.LogDir,
		syncClient:   NewTaskSyncClient(cfg.TaskSyncURL(), requestTimeout),
		taskManager:  deps.TaskManager,
		localTasks:   localTasks,
		availability: deps.Availability,
		onConnected:  deps.OnConnected,
		tracker:      tracker,
		uploader:     uploader,
		conn:         connMgr,
	}

	client.handler = NewInboundHandler(InboundHandlerDeps{
		TaskManager:  deps.TaskManager,
		RunRepo:      deps.RunRepo,
		LogDir:       deps.LogDir,
		Availability: deps.Availability,
		QueueExecUpdate: func(update protocol.ExecutionUpdateMessage) {
			client.tracker.QueueUpdate(update, connMgr.sendIfReady)
		},
		Uploader:       uploader,
		Tracker:        tracker,
		RequestRestart: deps.RequestRestart,
	})

	client.sessions = &sessionRunner{
		handler:     client.handler,
		systemStats: makeSystemStatsProvider(deps.SystemStats),
	}

	client.bridge = NewEventBridge(
		deps.EventBus,
		client.handler,
		client.tracker,
		connMgr.sendIfReady,
	)
	slog.Info("cloud integration enabled", "baseURL", cfg.BaseURL.String(), "fingerprint", cfg.Fingerprint)

	return client, nil
}

// RecoverArchiveBacklog re-runs uploads for executions whose dispatch was
// persisted but never archived (typically after a daemon crash mid-run).
// On success the resulting terminal `execution:update` is queued for
// delivery once the cloud session is up. Safe to call before Run.
func (client *Client) RecoverArchiveBacklog(ctx context.Context) {
	if client == nil || client.uploader == nil {
		return
	}
	client.uploader.RecoverOrphans(ctx, func(executionID string, result LogUploaderResult) {
		// Build a synthetic terminal update from the run record, then
		// overlay the recovered logPath/logSize. We re-fetch via the
		// run repo to get the canonical end_reason/exit_code.
		run, err := client.runRepo.GetRunByExternalExecutionID(ctx, executionID)
		if err != nil || run == nil {
			return
		}
		update := mapRunToExecutionUpdate(run)
		if update == nil {
			return
		}
		update.LogPath = result.LogPath
		update.LogSize = result.LogSize
		client.tracker.QueueUpdate(*update, client.conn.sendIfReady)
	})
}

func (client *Client) Run(ctx context.Context) error {
	if client == nil {
		return nil
	}
	client.bridge.Start(ctx)
	defer client.bridge.Shutdown()

	backoff := newReconnectBackoff()

	for {
		if ctx.Err() != nil {
			return nil
		}

		resetBackoff, err := client.runConnectionAttempt(ctx)
		if err != nil {
			if isHardAuthError(err) {
				slog.Error("cloud integration stopped due to hard auth error", "error", err.Error())
				return err
			}
			slog.Warn("cloud connection attempt ended", "error", err.Error())
		}

		if resetBackoff {
			backoff.Reset()
		}

		if ctx.Err() != nil {
			return nil
		}

		delay := backoff.NextBackOff()
		slog.Info("reconnecting", "delay", delay.String())

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
	}
}

func (client *Client) runConnectionAttempt(ctx context.Context) (bool, error) {
	connection, err := client.dialWebSocket(ctx)
	if err != nil {
		return false, err
	}

	if err := client.authenticate(ctx, connection); err != nil {
		closeConnection(connection, "auth failed")
		return false, err
	}

	if err := client.syncTasks(ctx, connection); err != nil {
		closeConnection(connection, "task sync failed")
		return false, err
	}

	// Only reset the reconnect backoff if the session actually stayed up. A peer
	// that resets the connection immediately after sync would otherwise reset
	// backoff on every attempt, turning the exponential curve into a tight
	// reconnect loop that hammers a flapping control plane. startSession blocks
	// until the session ends, so its wall-clock span is the session's lifetime.
	sessionStart := time.Now()
	sessionErr := client.startSession(ctx, connection)
	stable := time.Since(sessionStart) >= minStableSessionDuration
	return stable, sessionErr
}

func (client *Client) dialWebSocket(ctx context.Context) (*websocket.Conn, error) {
	headers := http.Header{}
	headers.Set("Authorization", fmt.Sprintf("Bearer %s", client.config.CloudToken))
	headers.Set("X-Runner-Agent-Version", client.config.AgentVersion)
	headers.Set("X-Runner-Fingerprint", client.config.Fingerprint)
	if capJSON, err := json.Marshal(client.availability); err == nil {
		headers.Set("X-Runner-Capabilities", string(capJSON))
	}
	// Advertise the cloud→daemon message types this daemon understands so the
	// cloud can gate features that depend on the daemon acting (and fall back
	// for older daemons that lack the type). Derived from inboundDecoders so it
	// can never drift from what we actually handle. See the forward-compat
	// contract atop packages/asyncapi/asyncapi.yaml.
	if featJSON, err := json.Marshal(supportedInboundTypes()); err == nil {
		headers.Set("X-Runner-Protocol-Features", string(featJSON))
	}

	dialCtx, cancelDial := context.WithTimeout(ctx, requestTimeout)
	defer cancelDial()

	connection, _, err := websocket.Dial(dialCtx, client.config.WebSocketURL(), &websocket.DialOptions{
		HTTPHeader: headers,
	})
	if err != nil {
		return nil, &CloudError{Kind: CloudErrorKindTransient, Message: "websocket dial failed", Err: err}
	}
	return connection, nil
}

func (client *Client) authenticate(ctx context.Context, connection *websocket.Conn) error {
	authResult, err := readAuthResult(ctx, connection)
	if err != nil {
		return err
	}

	if !authResult.Success {
		return &CloudError{Kind: CloudErrorKindAuth, Message: authResult.Error}
	}

	return nil
}

func (client *Client) syncTasks(ctx context.Context, connection *websocket.Conn) error {
	syncCtx, cancelSync := context.WithTimeout(ctx, requestTimeout)
	syncResult, syncErr := client.syncClient.SyncTasks(syncCtx, client.config.CloudToken, client.snapshotForSync())
	cancelSync()
	if syncErr != nil {
		return syncErr
	}

	slog.Info(
		"task snapshot synced",
		"added", syncResult.Summary.Added,
		"updated", syncResult.Summary.Updated,
		"markedOutOfSync", syncResult.Summary.MarkedOutOfSync,
		"total", syncResult.Summary.Total,
	)
	return nil
}

// snapshotForSync builds the task set reported in tasks.sync: the TOML-loaded
// local tasks plus every daemon-supervised service the task manager currently
// holds that the TOML doesn't already cover. The extras are cloud-declared
// services (registered at runtime via service:apply, named "cloud-<id>"); folding
// them in tells the cloud they are live on this runner, which gates their
// deletion. A cold-start's first sync runs before any service:apply has landed,
// so a freshly declared service is confirmed on the next sync (reconnect or
// TOML reload), not this one — the task manager retains it across reconnects.
func (client *Client) snapshotForSync() map[string]*model.Task {
	snapshot := make(map[string]*model.Task, len(client.localTasks))
	for name, task := range client.localTasks {
		snapshot[name] = task
	}
	if client.taskManager != nil {
		for _, svc := range client.taskManager.ListServiceTasks() {
			if svc == nil {
				continue
			}
			if _, ok := snapshot[svc.Name]; ok {
				continue // TOML already covers it with its richer definition.
			}
			snapshot[svc.Name] = svc
		}
	}
	return snapshot
}

func (client *Client) startSession(ctx context.Context, connection *websocket.Conn) error {
	session := &wsSession{
		conn:     connection,
		outbound: make(chan []byte, outboundMessageBufferSize),
	}
	session.lastReceived.Store(time.Now().UnixMilli())

	isFirstConnect := client.conn.attachSession(session)

	// Clear stale log listeners from a previous session. This is a safety net
	// in case the previous session's teardown was interrupted by a panic.
	if client.handler != nil {
		client.handler.ClearLogListeners()
	}
	if isFirstConnect && client.onConnected != nil {
		client.onConnected()
	}
	client.conn.flushPendingUpdates()
	// Immediately re-seed the control plane's service view on (re)connect: a
	// stable service emits no lifecycle event to trigger a fresh snapshot, so
	// without this the view stays empty until the first resend-ticker tick.
	if client.bridge != nil {
		client.bridge.EmitAllServiceStatus()
	}

	sessionErr := client.sessions.run(ctx, session)
	client.conn.detachSession()
	if client.handler != nil {
		client.handler.ClearLogListeners()
	}
	if sessionErr != nil {
		return &CloudError{Kind: CloudErrorKindTransient, Message: "connection lost", Err: sessionErr}
	}
	return nil
}

func closeConnection(conn *websocket.Conn, reason string) {
	if closeErr := conn.Close(websocket.StatusPolicyViolation, reason); closeErr != nil {
		slog.Warn("Failed to close connection", "err", closeErr)
	}
}

func sendMessage(session *wsSession, message any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return &CloudError{Kind: CloudErrorKindValidation, Message: "failed to encode outbound message", Err: err}
	}

	select {
	case session.outbound <- payload:
		return nil
	default:
		return &CloudError{Kind: CloudErrorKindTransient, Message: "outbound websocket queue is full"}
	}
}
