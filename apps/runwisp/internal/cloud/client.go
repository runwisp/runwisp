// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"github.com/coder/websocket"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/storage"
)

const (
	authReadTimeout            = 20 * time.Second
	heartbeatInterval          = 30 * time.Second
	heartbeatSilenceTimeout    = 45 * time.Second
	watchdogInterval           = 5 * time.Second
	writeTimeout               = 10 * time.Second
	outboundMessageBufferSize  = 256
	maxPendingExecutionUpdates = 2048
	maxInboundMessageSize      = 4 * 1024 * 1024 // 4 MiB
)

type Dependencies struct {
	TaskManager  runtime.TaskRunner
	RunRepo      storage.RunRepository
	EventBus     events.EventBus
	LocalTasks   map[string]*model.Task
	LogDir       string
	Availability executor.Availability
	OnConnected  func()
}

type Client struct {
	config  Config
	runRepo storage.RunRepository
	logDir  string

	syncClient   *TaskSyncClient
	localTasks   map[string]*model.Task
	availability executor.Availability
	onConnected  func()
	handler      *InboundHandler
	tracker      *ExecutionTracker
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

	client := &Client{
		config:       cfg,
		runRepo:      deps.RunRepo,
		logDir:       deps.LogDir,
		syncClient:   NewTaskSyncClient(cfg.TaskSyncURL(), cfg.TaskSyncTimeout),
		localTasks:   localTasks,
		availability: deps.Availability,
		onConnected:  deps.OnConnected,
		tracker:      tracker,
		conn:         connMgr,
	}

	client.handler = NewInboundHandler(
		deps.TaskManager,
		deps.RunRepo,
		deps.LogDir,
		deps.Availability,
		func(update protocol.ExecutionUpdateMessage) {
			client.tracker.QueueUpdate(update, connMgr.sendIfReady)
		},
	)

	client.sessions = &sessionRunner{handler: client.handler}

	client.bridge = NewEventBridge(
		deps.EventBus,
		client.handler,
		client.tracker,
		connMgr.sendIfReady,
		connMgr.refreshExecutionState,
	)
	client.bridge.Subscribe()
	log.Info("cloud integration enabled", "baseURL", cfg.BaseURL.String(), "fingerprint", cfg.Fingerprint)

	return client, nil
}

func (client *Client) Run(ctx context.Context) error {
	if client == nil {
		return nil
	}
	defer client.bridge.Shutdown()

	backoff := newReconnectBackoff()

	for {
		if ctx.Err() != nil {
			return nil
		}

		resetBackoff, err := client.runConnectionAttempt(ctx)
		if err != nil {
			if isHardAuthError(err) {
				log.Info("cloud integration stopped due to hard auth error", "error", err.Error())
				return err
			}
			log.Info("cloud connection attempt ended", "error", err.Error())
		}

		if resetBackoff {
			backoff.Reset()
		}

		if ctx.Err() != nil {
			return nil
		}

		client.conn.setState(StateReconnecting)
		delay := backoff.NextBackOff()
		log.Info("reconnecting", "delay", delay.String())

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
	}
}

func (client *Client) runConnectionAttempt(ctx context.Context) (bool, error) {
	client.conn.setState(StateConnecting)

	connection, err := client.dialWebSocket(ctx)
	if err != nil {
		client.conn.setState(StateConnectingFailed)
		return false, err
	}

	if err := client.authenticate(ctx, connection); err != nil {
		client.conn.setState(StateConnectingFailed)
		closeConnection(connection, "auth failed")
		return false, err
	}

	if err := client.syncTasks(ctx, connection); err != nil {
		client.conn.setState(StateConnectingFailed)
		closeConnection(connection, "task sync failed")
		return false, err
	}

	return true, client.startSession(ctx, connection)
}

func (client *Client) dialWebSocket(ctx context.Context) (*websocket.Conn, error) {
	headers := http.Header{}
	headers.Set("Authorization", fmt.Sprintf("Bearer %s", client.config.CloudToken))
	headers.Set("X-Runner-Agent-Version", client.config.AgentVersion)
	headers.Set("X-Runner-Fingerprint", client.config.Fingerprint)
	if capJSON, err := json.Marshal(client.availability); err == nil {
		headers.Set("X-Runner-Capabilities", string(capJSON))
	}

	dialCtx, cancelDial := context.WithTimeout(ctx, client.config.RequestTimeout)
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

	client.conn.setConnectionID(authResult.ConnectionID)
	client.conn.setState(StateAuthenticated)
	return nil
}

func (client *Client) syncTasks(ctx context.Context, connection *websocket.Conn) error {
	client.conn.setState(StateSyncing)
	syncCtx, cancelSync := context.WithTimeout(ctx, client.config.TaskSyncTimeout)
	syncResult, syncErr := client.syncClient.SyncTasks(syncCtx, client.config.CloudToken, client.localTasks)
	cancelSync()
	if syncErr != nil {
		return syncErr
	}

	log.Info(
		"task snapshot synced",
		"added", syncResult.Summary.Added,
		"updated", syncResult.Summary.Updated,
		"markedOutOfSync", syncResult.Summary.MarkedOutOfSync,
		"total", syncResult.Summary.Total,
	)
	return nil
}

func (client *Client) startSession(ctx context.Context, connection *websocket.Conn) error {
	session := &wsSession{
		conn:     connection,
		outbound: make(chan []byte, outboundMessageBufferSize),
	}
	session.lastReceived.Store(time.Now().UnixMilli())

	change := client.conn.attachSession(session)

	// Clear stale log listeners from a previous session. This is a safety net
	// in case the previous session's teardown was interrupted by a panic.
	if client.handler != nil {
		client.handler.ClearLogListeners()
	}
	if change.IsFirstConnect && client.onConnected != nil {
		client.onConnected()
	}
	client.conn.flushPendingUpdates()

	sessionErr := client.sessions.run(ctx, session)
	client.conn.detachSession()
	// Clean up log listeners registered during this now-ended session.
	if client.handler != nil {
		client.handler.ClearLogListeners()
	}
	if sessionErr != nil {
		client.conn.setState(StateConnectingFailed)
		return &CloudError{Kind: CloudErrorKindTransient, Message: "connection lost", Err: sessionErr}
	}
	return nil
}

func closeConnection(conn *websocket.Conn, reason string) {
	if closeErr := conn.Close(websocket.StatusPolicyViolation, reason); closeErr != nil {
		log.Warn("Failed to close connection", "err", closeErr)
	}
}

func sendMessage(session *wsSession, message any) error {
	payload, err := EncodeOutboundMessage(message)
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
