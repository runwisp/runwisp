// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testEnv bundles the mock servers and client used by lifecycle tests.
type testEnv struct {
	wsServer    *httptest.Server
	syncServer  *httptest.Server
	client      *Client
	eventBus    *events.Bus
	taskManager runtime.TaskManager
}

func (te *testEnv) close() {
	te.wsServer.Close()
	te.syncServer.Close()
	te.taskManager.Shutdown()
}

type wsHandlerFunc func(conn *websocket.Conn)

// newTestEnv creates a full test harness with an in-process WebSocket server,
// an HTTP task-sync server, and a wired-up Client.
func newTestEnv(t *testing.T, wsHandler wsHandlerFunc) *testEnv {
	t.Helper()

	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
		if err != nil {
			t.Logf("ws accept error: %v", err)
			return
		}
		wsHandler(conn)
	}))

	syncServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"summary":{"added":0,"updated":0,"markedOutOfSync":0,"total":0}}`))
	}))

	baseURL, _ := url.Parse(wsServer.URL)

	bus := events.NewEventBus()
	mockExec := &testutil.MockExecutor{}
	jm := runtime.NewTaskManager(mockExec, bus, time.Now)
	mockRepo := &testutil.MockRunRepository{}

	cfg := Config{
		Enabled:      true,
		BaseURL:      baseURL,
		CloudToken:   "test-token",
		AgentVersion: "0.0.0-test",
		Fingerprint:  "test-fp",
	}

	client, err := NewClient(cfg, Dependencies{
		TaskManager:  &testTaskRunnerAdapter{inner: jm},
		RunRepo:      mockRepo,
		EventBus:     bus,
		LocalTasks:   nil,
		LogDir:       t.TempDir(),
		Availability: executor.Availability{},
	})
	require.NoError(t, err)

	// Override syncClient to point at our test server
	client.syncClient = NewTaskSyncClient(syncServer.URL, 5*time.Second)

	return &testEnv{
		wsServer:    wsServer,
		syncServer:  syncServer,
		client:      client,
		eventBus:    bus,
		taskManager: jm,
	}
}

func sendJSON(conn *websocket.Conn, msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return conn.Write(context.Background(), websocket.MessageText, data)
}

// ---------- readAuthResult tests ----------

func TestReadAuthResultSuccess(t *testing.T) {
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		_ = sendJSON(conn, protocol.AuthResultMessage{
			Type:         "auth:result",
			Success:      true,
			ConnectionID: "conn-123",
		})
		// Keep connection open briefly so the client read succeeds
		time.Sleep(100 * time.Millisecond)
	}))
	defer wsServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsServer.URL, nil)
	require.NoError(t, err)
	defer conn.Close(websocket.StatusNormalClosure, "")

	result, err := readAuthResult(ctx, conn)
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Equal(t, "conn-123", result.ConnectionID)
}

func TestReadAuthResultFailure(t *testing.T) {
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		_ = sendJSON(conn, protocol.AuthResultMessage{
			Type:    "auth:result",
			Success: false,
			Error:   "invalid token",
		})
		time.Sleep(100 * time.Millisecond)
	}))
	defer wsServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsServer.URL, nil)
	require.NoError(t, err)
	defer conn.Close(websocket.StatusNormalClosure, "")

	result, err := readAuthResult(ctx, conn)
	require.NoError(t, err)
	assert.False(t, result.Success)
	assert.Equal(t, "invalid token", result.Error)
}

func TestReadAuthResultTimeout(t *testing.T) {
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		// Never send auth result — client should time out
		time.Sleep(5 * time.Second)
	}))
	defer wsServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsServer.URL, nil)
	require.NoError(t, err)
	defer conn.Close(websocket.StatusNormalClosure, "")

	shortCtx, shortCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer shortCancel()

	_, err = readAuthResult(shortCtx, conn)
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindTransient, ce.Kind)
}

func TestReadAuthResultInvalidMessage(t *testing.T) {
	wsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		// Send a non-auth message as the first message
		_ = sendJSON(conn, protocol.PongMessage{Type: "pong"})
		time.Sleep(100 * time.Millisecond)
	}))
	defer wsServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsServer.URL, nil)
	require.NoError(t, err)
	defer conn.Close(websocket.StatusNormalClosure, "")

	_, err = readAuthResult(ctx, conn)
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindValidation, ce.Kind)
	assert.Contains(t, ce.Message, "auth:result")
}

// ---------- Connection lifecycle tests ----------

func TestConnectionAuthSuccessReachesReady(t *testing.T) {
	env := newTestEnv(t, func(conn *websocket.Conn) {
		defer conn.Close(websocket.StatusNormalClosure, "done")
		_ = sendJSON(conn, protocol.AuthResultMessage{
			Type:         "auth:result",
			Success:      true,
			ConnectionID: "conn-ok",
		})
		// Keep session alive; echo pongs to client pings
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var env struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(data, &env) == nil && env.Type == "ping" {
				_ = sendJSON(conn, protocol.PongMessage{Type: "pong"})
			}
		}
	})
	defer env.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() { _ = env.client.Run(ctx) }()

	// Wait until the client's session is attached and ready.
	require.Eventually(t, func() bool {
		env.client.conn.mu.Lock()
		defer env.client.conn.mu.Unlock()
		return env.client.conn.ready
	}, 3*time.Second, 50*time.Millisecond)

	cancel()
}

func TestConnectionAuthFailureReturnsError(t *testing.T) {
	env := newTestEnv(t, func(conn *websocket.Conn) {
		defer conn.Close(websocket.StatusPolicyViolation, "auth failed")
		_ = sendJSON(conn, protocol.AuthResultMessage{
			Type:    "auth:result",
			Success: false,
			Error:   "token revoked",
		})
	})
	defer env.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := env.client.Run(ctx)
	require.Error(t, err)
	assert.True(t, isHardAuthError(err))
}

func TestHeartbeatTimeoutTriggersReconnect(t *testing.T) {
	env := newTestEnv(t, func(conn *websocket.Conn) {
		conn.Close(websocket.StatusNormalClosure, "")
	})
	defer env.close()

	// Test watchdogLoop directly with a stale lastReceived timestamp.
	session := &wsSession{
		outbound: make(chan []byte, 1),
	}
	session.lastReceived.Store(time.Now().Add(-2 * heartbeatSilenceTimeout).UnixMilli())

	watchdogCtx, watchdogCancel := context.WithTimeout(context.Background(), watchdogInterval+3*time.Second)
	defer watchdogCancel()

	err := env.client.sessions.watchdogLoop(watchdogCtx, session)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "heartbeat timeout")
}

func TestWatchdogLoopRespectsContext(t *testing.T) {
	session := &wsSession{
		outbound: make(chan []byte, 1),
	}
	session.lastReceived.Store(time.Now().UnixMilli())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	env := newTestEnv(t, func(conn *websocket.Conn) {
		conn.Close(websocket.StatusNormalClosure, "")
	})
	defer env.close()

	err := env.client.sessions.watchdogLoop(ctx, session)
	assert.NoError(t, err)
}

func TestReconnectAfterSessionDrop(t *testing.T) {
	var connectCount atomic.Int32

	env := newTestEnv(t, func(conn *websocket.Conn) {
		n := connectCount.Add(1)
		_ = sendJSON(conn, protocol.AuthResultMessage{
			Type:         "auth:result",
			Success:      true,
			ConnectionID: "conn-drop",
		})
		if n == 1 {
			// First connection: close immediately to simulate drop
			conn.Close(websocket.StatusGoingAway, "simulated drop")
			return
		}
		// Second connection: stay alive
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			var env struct {
				Type string `json:"type"`
			}
			if json.Unmarshal(data, &env) == nil && env.Type == "ping" {
				_ = sendJSON(conn, protocol.PongMessage{Type: "pong"})
			}
		}
	})
	defer env.close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go func() { _ = env.client.Run(ctx) }()

	require.Eventually(t, func() bool {
		return connectCount.Load() >= 2
	}, 8*time.Second, 100*time.Millisecond, "client should have reconnected at least once")

	cancel()
}

// TestRunConnectionAttempt_ShortSessionDoesNotResetBackoff pins the M8 fix:
// runConnectionAttempt must only signal a backoff reset (its bool return) when
// the session actually stayed up for minStableSessionDuration. A peer that
// resets the connection immediately after sync used to reset backoff on every
// attempt, turning the exponential curve into a tight reconnect loop. Here the
// peer authenticates then closes at once, so the session lives far less than
// the stability threshold and the reset must be withheld. (The stable==true
// branch depends on a real ≥30s session, so it isn't unit-testable without an
// injected clock; this guards the flap side, which is where the bug lived.)
func TestRunConnectionAttempt_ShortSessionDoesNotResetBackoff(t *testing.T) {
	env := newTestEnv(t, func(conn *websocket.Conn) {
		_ = sendJSON(conn, protocol.AuthResultMessage{
			Type:         "auth:result",
			Success:      true,
			ConnectionID: "conn-flap",
		})
		// Close immediately after auth to mimic a control plane that flaps right
		// after task sync — the session lasts only milliseconds.
		conn.Close(websocket.StatusGoingAway, "flap")
	})
	defer env.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	resetBackoff, _ := env.client.runConnectionAttempt(ctx)
	elapsed := time.Since(start)

	require.Less(t, elapsed, minStableSessionDuration,
		"test premise: the session must end well before the stability threshold")
	assert.False(t, resetBackoff,
		"a session shorter than minStableSessionDuration must not signal a backoff reset")
}

// ---------- Backoff tests ----------

func TestReconnectBackoffIncreases(t *testing.T) {
	b := newReconnectBackoff()
	first := b.NextBackOff()
	second := b.NextBackOff()
	third := b.NextBackOff()
	// Due to jitter, we can't assert exact values, but the trend should increase
	assert.LessOrEqual(t, first, second+200*time.Millisecond)
	assert.LessOrEqual(t, second, third+500*time.Millisecond)
}

func TestReconnectBackoffReset(t *testing.T) {
	b := newReconnectBackoff()
	for i := 0; i < 10; i++ {
		b.NextBackOff()
	}
	b.Reset()
	afterReset := b.NextBackOff()
	// After reset, delay should be close to the initial interval
	assert.Less(t, afterReset, 2*time.Second)
}

// ---------- sendMessage tests ----------

func TestSendMessageQueueFull(t *testing.T) {
	env := newTestEnv(t, func(conn *websocket.Conn) {
		conn.Close(websocket.StatusNormalClosure, "")
	})
	defer env.close()

	session := &wsSession{
		outbound: make(chan []byte, 1),
	}
	session.outbound <- []byte("filler")

	err := sendMessage(session, NewPingMessage(nil))
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindTransient, ce.Kind)
	assert.Contains(t, ce.Message, "full")
}

// TestSessionClosed_AfterRunReturns_SendMessageFails is the regression test
// for a reconnect-vs-teardown race: connectionManager.detachSession() only
// runs after sessionRunner.run() returns to startSession, but by then
// writeLoop (the only reader of session.outbound) is already dead. Any
// caller racing that gap — EventBridge, which reacts to run events on its
// own goroutine independent of the session lifecycle — would see
// connectionManager still reporting ready+session, call sendMessage, and get
// a nil error even though the message can never be delivered (nothing will
// ever drain the channel). ExecutionTracker.QueueUpdate treats a nil error as
// "delivered" and never buffers it for retry, so the update is silently lost.
// This test proves sendMessage rejects sends on a session whose run() has
// already returned, closing that window without needing to hit the race
// timing directly.
func TestSessionClosed_AfterRunReturns_SendMessageFails(t *testing.T) {
	env := newTestEnv(t, func(conn *websocket.Conn) {
		for {
			if _, _, err := conn.Read(context.Background()); err != nil {
				return
			}
		}
	})
	defer env.close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := env.client.dialWebSocket(ctx)
	require.NoError(t, err)

	session := &wsSession{conn: conn, outbound: make(chan []byte, 4)}
	session.lastReceived.Store(time.Now().UnixMilli())

	runCtx, runCancel := context.WithCancel(ctx)
	runCancel() // simulate the session ending (e.g. peer drop) immediately

	runErr := env.client.sessions.run(runCtx, session)
	require.NoError(t, runErr)

	sendErr := sendMessage(session, "late-update")
	require.Error(t, sendErr, "sendMessage on a session whose run() already returned must fail, not silently queue into a dead channel")
}

// ---------- WebSocket URL derivation ----------

func TestWebSocketURLDerivation(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		expected string
	}{
		{"https to wss", "https://app.runwisp.com", "wss://app.runwisp.com/api/v1/runner/ws"},
		{"http to ws", "http://localhost:3000", "ws://localhost:3000/api/v1/runner/ws"},
		{"strips path", "https://app.runwisp.com/foo", "wss://app.runwisp.com/api/v1/runner/ws"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, err := url.Parse(tt.baseURL)
			require.NoError(t, err)
			cfg := Config{BaseURL: u}
			assert.Equal(t, tt.expected, cfg.WebSocketURL())
		})
	}
}

// ---------- Inbound payload dispatch (via handler) ----------

func TestRecoverArchiveBacklog_NilClientReturnsImmediately(t *testing.T) {
	var c *Client
	// Must not panic on a nil receiver — RecoverArchiveBacklog is safe before Run().
	c.RecoverArchiveBacklog(context.Background())
}

func TestRecoverArchiveBacklog_NilUploaderReturnsImmediately(t *testing.T) {
	// Uploader is nil when LogUploader bootstrapping skipped; the recovery
	// path must short-circuit without panicking on the uploader field.
	c := &Client{}
	c.RecoverArchiveBacklog(context.Background())
}

// newClientWithUploader builds a Client wired with a real LogUploader, fake
// pending repo, and fake run repo — the minimum surface RecoverArchiveBacklog
// needs. We bypass NewClient because it requires a TaskRunner and event bus
// that aren't relevant to backlog recovery.
func newClientWithUploader(uploader *LogUploader, runRepo ExternalRunGetter) *Client {
	tracker := NewExecutionTracker()
	connMgr := newConnectionManager(tracker)
	return &Client{
		runRepo:  runRepo,
		uploader: uploader,
		tracker:  tracker,
		conn:     connMgr,
	}
}

func TestRecoverArchiveBacklog_EmptyBacklogIsNoop(t *testing.T) {
	repo := newFakePendingRepo()
	runs := &fakeRunRepo{byExt: map[string]*model.Run{}}
	u := NewLogUploader(repo, runs, t.TempDir(), fixedClock())

	c := newClientWithUploader(u, runs)
	// No rows, nothing to recover; must not panic.
	c.RecoverArchiveBacklog(context.Background())

	assert.Equal(t, 0, repo.count())
}

func TestRecoverArchiveBacklog_SuccessfulUploadQueuesTerminalUpdate(t *testing.T) {
	logDir := t.TempDir()
	run := newTerminalRun("greet", "01HX-BACKLOG", "exec-backlog-1")
	reason := model.ReasonSuccess
	run.EndReason = &reason
	writeRunLog(t, logDir, run, "recovered log body\n")

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	repo := newFakePendingRepo()
	require.NoError(t, repo.UpsertPendingLogUpload(context.Background(), model.PendingLogUpload{
		ExecutionID: "exec-backlog-1",
		UploadURL:   srv.URL,
		LogPath:     "archive/exec-backlog-1.log.gz",
		InsertedAt:  time.Now().Unix(),
	}))

	runs := &fakeRunRepo{byExt: map[string]*model.Run{"exec-backlog-1": run}}
	u := NewLogUploader(repo, runs, logDir, fixedClock())
	u.httpClient = srv.Client()

	c := newClientWithUploader(u, runs)

	c.RecoverArchiveBacklog(context.Background())

	// Persistence row cleared on success.
	assert.Equal(t, 0, repo.count(), "successful recovery must drop the pending row")

	// The terminal update is buffered because the connection isn't ready.
	c.tracker.mu.Lock()
	defer c.tracker.mu.Unlock()
	require.Len(t, c.tracker.pendingExecutionUpdates, 1)
	update := c.tracker.pendingExecutionUpdates[0]
	assert.Equal(t, "exec-backlog-1", update.ExecutionID)
	assert.Equal(t, "archive/exec-backlog-1.log.gz", update.LogPath)
	assert.Greater(t, update.LogSize, int64(0))
}

func TestRecoverArchiveBacklog_RunMissingDropsRow(t *testing.T) {
	repo := newFakePendingRepo()
	require.NoError(t, repo.UpsertPendingLogUpload(context.Background(), model.PendingLogUpload{
		ExecutionID: "exec-orphan",
		UploadURL:   "https://upload.invalid/x",
		LogPath:     "archive/exec-orphan.log.gz",
		InsertedAt:  time.Now().Unix(),
	}))

	runs := &fakeRunRepo{byExt: map[string]*model.Run{}} // ErrNotFound for every lookup
	u := NewLogUploader(repo, runs, t.TempDir(), fixedClock())

	c := newClientWithUploader(u, runs)
	c.RecoverArchiveBacklog(context.Background())

	assert.Equal(t, 0, repo.count(), "orphan rows must be dropped when the run is gone")
	c.tracker.mu.Lock()
	defer c.tracker.mu.Unlock()
	assert.Empty(t, c.tracker.pendingExecutionUpdates, "no update should be queued when the run is missing")
}

// ---------- writeLoop tests ----------

// startWriteLoopServer accepts a single WebSocket connection and pushes every
// received payload to msgs. It returns the server, client conn, and a cleanup
// for them both. Used to exercise writeLoop end-to-end.
func startWriteLoopServer(t *testing.T, msgs chan<- []byte) (*httptest.Server, *websocket.Conn) {
	t.Helper()
	serverReady := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
		if err != nil {
			t.Logf("ws accept: %v", err)
			return
		}
		close(serverReady)
		for {
			_, payload, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			select {
			case msgs <- payload:
			default:
			}
		}
	}))

	clientConn, _, err := websocket.Dial(context.Background(), strings.Replace(srv.URL, "http", "ws", 1), nil)
	require.NoError(t, err)
	<-serverReady
	return srv, clientConn
}

func TestWriteLoop_DeliversQueuedPayload(t *testing.T) {
	msgs := make(chan []byte, 4)
	srv, clientConn := startWriteLoopServer(t, msgs)
	defer srv.Close()
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	session := &wsSession{
		conn:     clientConn,
		outbound: make(chan []byte, 4),
	}
	session.outbound <- []byte(`{"type":"ping"}`)

	sr := &sessionRunner{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- sr.writeLoop(ctx, session) }()

	select {
	case got := <-msgs:
		assert.Equal(t, `{"type":"ping"}`, string(got), "writeLoop must forward the queued payload to the peer")
	case <-time.After(2 * time.Second):
		t.Fatal("peer never received the payload")
	}

	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err, "writeLoop must exit cleanly on context cancel")
	case <-time.After(2 * time.Second):
		t.Fatal("writeLoop did not exit after context cancel")
	}
}

func TestWriteLoop_ContextCancelExitsCleanly(t *testing.T) {
	msgs := make(chan []byte, 1)
	srv, clientConn := startWriteLoopServer(t, msgs)
	defer srv.Close()
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	session := &wsSession{
		conn:     clientConn,
		outbound: make(chan []byte, 1),
	}
	sr := &sessionRunner{}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- sr.writeLoop(ctx, session) }()

	cancel()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("writeLoop did not exit after context cancel")
	}
}

// ---------- NewClient validation ----------

func TestNewClient_DisabledReturnsNil(t *testing.T) {
	client, err := NewClient(Config{Enabled: false}, Dependencies{})
	require.NoError(t, err)
	assert.Nil(t, client, "disabled config must produce no client")
}

func TestNewClient_MissingTaskManager(t *testing.T) {
	_, err := NewClient(Config{Enabled: true}, Dependencies{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "task manager")
}

func TestNewClient_MissingRunRepo(t *testing.T) {
	bus := events.NewEventBus()
	mockExec := &testutil.MockExecutor{}
	jm := runtime.NewTaskManager(mockExec, bus, time.Now)
	defer jm.Shutdown()

	_, err := NewClient(Config{Enabled: true}, Dependencies{
		TaskManager: &testTaskRunnerAdapter{inner: jm},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "run repository")
}

func TestNewClient_MissingEventBus(t *testing.T) {
	bus := events.NewEventBus()
	mockExec := &testutil.MockExecutor{}
	jm := runtime.NewTaskManager(mockExec, bus, time.Now)
	defer jm.Shutdown()

	_, err := NewClient(Config{Enabled: true}, Dependencies{
		TaskManager: &testTaskRunnerAdapter{inner: jm},
		RunRepo:     &testutil.MockRunRepository{},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "event bus")
}

func TestNewClient_SkipsNilLocalTask(t *testing.T) {
	baseURL, _ := url.Parse("http://localhost:0")
	bus := events.NewEventBus()
	mockExec := &testutil.MockExecutor{}
	jm := runtime.NewTaskManager(mockExec, bus, time.Now)
	defer jm.Shutdown()

	original := &model.Task{Name: "alpha"}
	registry := runtime.NewTaskRegistry(map[string]*model.Task{"alpha": original, "beta": nil})
	client, err := NewClient(Config{
		Enabled: true,
		BaseURL: baseURL,
	}, Dependencies{
		TaskManager: &testTaskRunnerAdapter{inner: jm},
		RunRepo:     &testutil.MockRunRepository{},
		EventBus:    bus,
		LocalTasks:  registry,
		LogDir:      t.TempDir(),
	})
	require.NoError(t, err)
	require.NotNil(t, client)

	snapshot := client.snapshotForSync()
	require.Contains(t, snapshot, "alpha")
	assert.NotContains(t, snapshot, "beta")
	assert.Equal(t, "alpha", snapshot["alpha"].Name)
}

// TestSnapshotForSync_ReflectsLiveReload is the bug: a `runwisp reload` that
// adds/renames/removes tasks mutates the daemon's live TaskRegistry without a
// process restart, but the cloud client used to snapshot that registry once
// at startCloudClient time. tasks.sync kept reporting the boot-time task set
// forever, even across reconnects, until the whole daemon process restarted.
func TestSnapshotForSync_ReflectsLiveReload(t *testing.T) {
	baseURL, _ := url.Parse("http://localhost:0")
	bus := events.NewEventBus()
	mockExec := &testutil.MockExecutor{}
	jm := runtime.NewTaskManager(mockExec, bus, time.Now)
	defer jm.Shutdown()

	registry := runtime.NewTaskRegistry(map[string]*model.Task{"backup": {Name: "backup"}})
	client, err := NewClient(Config{
		Enabled: true,
		BaseURL: baseURL,
	}, Dependencies{
		TaskManager: &testTaskRunnerAdapter{inner: jm},
		RunRepo:     &testutil.MockRunRepository{},
		EventBus:    bus,
		LocalTasks:  registry,
		LogDir:      t.TempDir(),
	})
	require.NoError(t, err)

	before := client.snapshotForSync()
	require.Contains(t, before, "backup")
	require.NotContains(t, before, "cleanup")

	// Simulate `runwisp reload`: rename backup -> nightly-backup, add cleanup.
	registry.Delete("backup")
	registry.Set(&model.Task{Name: "nightly-backup"})
	registry.Set(&model.Task{Name: "cleanup"})

	after := client.snapshotForSync()
	assert.NotContains(t, after, "backup", "reload removed this task; sync must stop reporting it")
	assert.Contains(t, after, "nightly-backup")
	assert.Contains(t, after, "cleanup")
}

// Nil-receiver Run is a safe no-op so callers can wire the cloud client
// unconditionally and let NewClient's disabled-config check return nil.
func TestRun_NilClientIsNoop(t *testing.T) {
	var c *Client
	require.NoError(t, c.Run(context.Background()))
}

// readLoop reads frames until the websocket closes, then returns the wrapped
// error. We push a single inbound pong, then close the server side and assert
// the readLoop exits with a wrapped error.
func TestReadLoop_PassesMessageThroughThenExitsOnClose(t *testing.T) {
	env := newTestEnv(t, func(conn *websocket.Conn) {
		_ = sendJSON(conn, protocol.PongMessage{Type: "pong"})
		// Hold briefly so the client reads, then close.
		time.Sleep(100 * time.Millisecond)
		conn.Close(websocket.StatusNormalClosure, "done")
	})
	defer env.close()

	clientConn, _, err := websocket.Dial(context.Background(), strings.Replace(env.wsServer.URL, "http", "ws", 1), nil)
	require.NoError(t, err)
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	session := &wsSession{
		conn:     clientConn,
		outbound: make(chan []byte, 4),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	loopErr := env.client.sessions.readLoop(ctx, session)
	require.Error(t, loopErr, "readLoop must surface the websocket close as an error")
	assert.Contains(t, loopErr.Error(), "read loop")
	// The pong frame must have updated lastReceived.
	assert.NotZero(t, session.lastReceived.Load(), "lastReceived should be stamped on inbound traffic")
}

func TestReadLoop_ExitsOnContextCancel(t *testing.T) {
	env := newTestEnv(t, func(conn *websocket.Conn) {
		// Hold the connection open; the client cancels the context instead.
		time.Sleep(2 * time.Second)
		conn.Close(websocket.StatusNormalClosure, "done")
	})
	defer env.close()

	clientConn, _, err := websocket.Dial(context.Background(), strings.Replace(env.wsServer.URL, "http", "ws", 1), nil)
	require.NoError(t, err)
	defer clientConn.Close(websocket.StatusNormalClosure, "")

	session := &wsSession{conn: clientConn, outbound: make(chan []byte, 4)}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so the read fails immediately

	loopErr := env.client.sessions.readLoop(ctx, session)
	require.Error(t, loopErr)
}

// closeConnection logs (rather than panics) when the underlying Close already
// returned its error — exercised by closing twice.
func TestCloseConnection_AlreadyClosedIsLogged(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{})
		if err != nil {
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		time.Sleep(50 * time.Millisecond)
	}))
	defer srv.Close()

	conn, _, err := websocket.Dial(context.Background(), strings.Replace(srv.URL, "http", "ws", 1), nil)
	require.NoError(t, err)

	// First close succeeds; second close hits the error branch in closeConnection.
	closeConnection(conn, "first")
	closeConnection(conn, "second-already-closed")
}

// sendProtocolError swallows send errors (best-effort). Cover the error branch
// by filling the outbound buffer before invoking it.
func TestSendProtocolError_DropsOnFullBuffer(t *testing.T) {
	env := newTestEnv(t, func(conn *websocket.Conn) {
		conn.Close(websocket.StatusNormalClosure, "")
	})
	defer env.close()

	session := &wsSession{outbound: make(chan []byte, 1)}
	session.outbound <- []byte("filler")

	// Must not panic — the function logs and returns on send failure.
	env.client.sessions.sendProtocolError(session, CloudErrorKindValidation, "boom", "req-1", "")
}

func TestHeartbeatLoop_ContextCancelExitsCleanly(t *testing.T) {
	sr := &sessionRunner{}
	session := &wsSession{outbound: make(chan []byte, 1)}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := sr.heartbeatLoop(ctx, session)
	assert.NoError(t, err)
}

func TestWriteLoop_WriteErrorReturnsErr(t *testing.T) {
	msgs := make(chan []byte, 1)
	srv, clientConn := startWriteLoopServer(t, msgs)
	defer srv.Close()

	session := &wsSession{
		conn:     clientConn,
		outbound: make(chan []byte, 1),
	}

	// Forcibly close the connection so the subsequent Write errors out.
	_ = clientConn.Close(websocket.StatusNormalClosure, "force close")

	session.outbound <- []byte(`{"type":"ping"}`)

	sr := &sessionRunner{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- sr.writeLoop(ctx, session) }()

	select {
	case err := <-done:
		require.Error(t, err, "writeLoop must return an error when the underlying Write fails")
		assert.Contains(t, err.Error(), "write loop")
	case <-time.After(2 * time.Second):
		t.Fatal("writeLoop did not exit after write error")
	}
}

func TestHandleInboundPayloadPong(t *testing.T) {
	env := newTestEnv(t, func(conn *websocket.Conn) {
		conn.Close(websocket.StatusNormalClosure, "")
	})
	defer env.close()

	session := &wsSession{outbound: make(chan []byte, 16)}
	payload, _ := json.Marshal(protocol.PongMessage{Type: "pong"})
	err := env.client.sessions.handleInboundPayload(context.Background(), session, payload)
	assert.NoError(t, err)
}

// An unsupported (future) cloud→daemon message type must be ignored cleanly:
// the session survives (no error bubbled up to tear it down) and NO protocol
// error frame is sent back. This is the forward-compat contract — a message a
// newer cloud sends that this daemon doesn't implement is not a validation
// failure. See sessionRunner.handleInboundPayload.
func TestHandleInboundPayloadUnsupportedType(t *testing.T) {
	env := newTestEnv(t, func(conn *websocket.Conn) {
		conn.Close(websocket.StatusNormalClosure, "")
	})
	defer env.close()

	session := &wsSession{outbound: make(chan []byte, 16)}
	payload := []byte(`{"type":"future:feature","someNewField":42}`)
	err := env.client.sessions.handleInboundPayload(context.Background(), session, payload)
	assert.NoError(t, err, "unsupported type must not tear down the session")

	select {
	case out := <-session.outbound:
		t.Fatalf("unsupported type must not produce an error frame, got: %s", string(out))
	default:
	}
}

func TestHandleInboundPayloadInvalidJSON(t *testing.T) {
	env := newTestEnv(t, func(conn *websocket.Conn) {
		conn.Close(websocket.StatusNormalClosure, "")
	})
	defer env.close()

	session := &wsSession{outbound: make(chan []byte, 16)}
	err := env.client.sessions.handleInboundPayload(context.Background(), session, []byte("not json"))
	assert.Error(t, err)
}

func TestHandleInboundPayloadProtocolError(t *testing.T) {
	env := newTestEnv(t, func(conn *websocket.Conn) {
		conn.Close(websocket.StatusNormalClosure, "")
	})
	defer env.close()

	session := &wsSession{outbound: make(chan []byte, 16)}
	payload := []byte(`{"type":"error","code":"validation","message":"bad","requestId":"req-1"}`)
	err := env.client.sessions.handleInboundPayload(context.Background(), session, payload)
	assert.NoError(t, err)
}

// A dispatch frame that omits the required `execution` field decodes to a nil
// pointer (decodeStrict validates JSON syntax, not `binding:"required"`). The
// handler must reject it with a validation frame, never deref nil — an
// unrecovered panic here runs in the read-loop goroutine and would crash the
// entire daemon, not just the cloud session.
func TestHandleInboundPayloadDispatchMissingExecution(t *testing.T) {
	env := newTestEnv(t, func(conn *websocket.Conn) {
		conn.Close(websocket.StatusNormalClosure, "")
	})
	defer env.close()

	session := &wsSession{outbound: make(chan []byte, 16)}
	payload := []byte(`{"type":"execution:dispatch"}`)
	err := env.client.sessions.handleInboundPayload(context.Background(), session, payload)
	assert.NoError(t, err, "missing execution must not tear down the session")

	select {
	case out := <-session.outbound:
		assert.Contains(t, string(out), "execution is required")
	default:
		t.Fatal("expected a validation error frame for the missing execution field")
	}
}

// The nil guards above cover the frame shapes we know about. This covers the
// next one: whatever panics below the dispatch funnel must come back as an
// error, because handleInboundPayload runs on the read-loop goroutine and an
// unrecovered panic there takes down the daemon — every supervised service with
// it — over one frame from an optional integration. A nil handler stands in for
// an arbitrary panicking handler.
func TestHandleInboundPayloadRecoversFromPanic(t *testing.T) {
	sr := &sessionRunner{}
	session := &wsSession{outbound: make(chan []byte, 16)}
	payload := []byte(`{"type":"service:apply","service":{"taskId":"web","taskName":"web"}}`)

	err := sr.handleInboundPayload(context.Background(), session, payload)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "panic handling inbound message")

	select {
	case out := <-session.outbound:
		assert.Contains(t, string(out), "message could not be processed")
	default:
		t.Fatal("expected a validation error frame after the recovered panic")
	}
}

func TestHandleInboundPayloadAuthResultSuccess(t *testing.T) {
	env := newTestEnv(t, func(conn *websocket.Conn) {
		conn.Close(websocket.StatusNormalClosure, "")
	})
	defer env.close()

	session := &wsSession{outbound: make(chan []byte, 16)}
	payload := []byte(`{"type":"auth:result","success":true,"connectionId":"c1"}`)
	err := env.client.sessions.handleInboundPayload(context.Background(), session, payload)
	assert.NoError(t, err)
}

func TestHandleInboundPayloadAuthResultFailureReturnsError(t *testing.T) {
	env := newTestEnv(t, func(conn *websocket.Conn) {
		conn.Close(websocket.StatusNormalClosure, "")
	})
	defer env.close()

	session := &wsSession{outbound: make(chan []byte, 16)}
	payload := []byte(`{"type":"auth:result","success":false,"error":"bad token"}`)
	err := env.client.sessions.handleInboundPayload(context.Background(), session, payload)
	require.Error(t, err)
	ce, ok := err.(*CloudError)
	require.True(t, ok)
	assert.Equal(t, CloudErrorKindAuth, ce.Kind)
	assert.Equal(t, "bad token", ce.Message)
}

func TestHandleInboundPayloadLogListenMissingExecID(t *testing.T) {
	env := newTestEnv(t, func(conn *websocket.Conn) {
		conn.Close(websocket.StatusNormalClosure, "")
	})
	defer env.close()

	session := &wsSession{outbound: make(chan []byte, 16)}
	// Empty executionId triggers the validation error branch in HandleLogListen,
	// which the dispatcher converts to a protocol error response.
	payload := []byte(`{"type":"log:listen","executionId":""}`)
	err := env.client.sessions.handleInboundPayload(context.Background(), session, payload)
	assert.NoError(t, err, "validation errors are sent back to peer, not bubbled up")

	// Verify the outbound queue received a protocol-error message.
	select {
	case out := <-session.outbound:
		assert.Contains(t, string(out), `"type":"error"`)
	default:
		t.Fatal("expected protocol error to be queued for the peer")
	}
}

func TestHandleInboundPayloadLogListenSuccessAndStop(t *testing.T) {
	env := newTestEnv(t, func(conn *websocket.Conn) {
		conn.Close(websocket.StatusNormalClosure, "")
	})
	defer env.close()

	session := &wsSession{outbound: make(chan []byte, 16)}

	payload := []byte(`{"type":"log:listen","executionId":"exec-1"}`)
	err := env.client.sessions.handleInboundPayload(context.Background(), session, payload)
	assert.NoError(t, err)
	assert.True(t, env.client.handler.IsLogListener("exec-1"))

	// log:stop must remove the listener.
	payload = []byte(`{"type":"log:stop","executionId":"exec-1"}`)
	err = env.client.sessions.handleInboundPayload(context.Background(), session, payload)
	assert.NoError(t, err)
	assert.False(t, env.client.handler.IsLogListener("exec-1"))
}

func TestHandleInboundPayloadExecutionStopMissingExecID(t *testing.T) {
	env := newTestEnv(t, func(conn *websocket.Conn) {
		conn.Close(websocket.StatusNormalClosure, "")
	})
	defer env.close()

	session := &wsSession{outbound: make(chan []byte, 16)}
	// Empty executionId fails validation inside HandleExecutionStop, dispatcher
	// sends a protocol error back without bubbling up.
	payload := []byte(`{"type":"execution:stop","executionId":""}`)
	err := env.client.sessions.handleInboundPayload(context.Background(), session, payload)
	assert.NoError(t, err)
	select {
	case out := <-session.outbound:
		assert.Contains(t, string(out), `"type":"error"`)
	default:
		t.Fatal("expected protocol error to be queued for the peer")
	}
}

func TestHandleInboundPayloadExecutionDispatchInvalid(t *testing.T) {
	env := newTestEnv(t, func(conn *websocket.Conn) {
		conn.Close(websocket.StatusNormalClosure, "")
	})
	defer env.close()

	session := &wsSession{outbound: make(chan []byte, 16)}
	// Empty Execution.executionId fails handler-side validation. The dispatcher
	// must convert that to a protocol error rather than bubbling up.
	payload := []byte(`{"type":"execution:dispatch","execution":{"executionId":""}}`)
	err := env.client.sessions.handleInboundPayload(context.Background(), session, payload)
	assert.NoError(t, err)
	select {
	case out := <-session.outbound:
		assert.Contains(t, string(out), `"type":"error"`)
	default:
		t.Fatal("expected protocol error to be queued for the peer")
	}
}

func TestHandleInboundPayloadLogReplayRequestMissingExecID(t *testing.T) {
	env := newTestEnv(t, func(conn *websocket.Conn) {
		conn.Close(websocket.StatusNormalClosure, "")
	})
	defer env.close()

	session := &wsSession{outbound: make(chan []byte, 16)}
	payload := []byte(`{"type":"log:replayRequest","id":"req-1","executionId":""}`)
	err := env.client.sessions.handleInboundPayload(context.Background(), session, payload)
	assert.NoError(t, err)
	// One frame per request: the protocol error only — no empty replay chunk
	// trails it.
	gotError := false
	gotReply := false
	for i := 0; i < 2; i++ {
		select {
		case out := <-session.outbound:
			s := string(out)
			if strings.Contains(s, `"type":"error"`) {
				gotError = true
			}
			if strings.Contains(s, `"type":"log:replayChunk"`) {
				gotReply = true
			}
		default:
		}
	}
	assert.True(t, gotError, "validation error should be queued")
	assert.False(t, gotReply, "no replay chunk should follow the error frame")
}

// ---------- Error classification ----------

func TestIsHardAuthError(t *testing.T) {
	assert.True(t, isHardAuthError(&CloudError{Kind: CloudErrorKindAuth}))
	assert.False(t, isHardAuthError(&CloudError{Kind: CloudErrorKindTransient}))
	assert.False(t, isHardAuthError(context.DeadlineExceeded))
}

func TestClassifyErrorKind(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected CloudErrorKind
	}{
		{"auth error", &CloudError{Kind: CloudErrorKindAuth}, CloudErrorKindAuth},
		{"validation error", &CloudError{Kind: CloudErrorKindValidation}, CloudErrorKindValidation},
		{"non-cloud error defaults to transient", context.DeadlineExceeded, CloudErrorKindTransient},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, classifyErrorKind(tt.err))
		})
	}
}

// ---------- WebSocket URL path injection safety ----------

func TestWebSocketURLPathInjectionSafety(t *testing.T) {
	u, _ := url.Parse("https://app.runwisp.com/malicious/../admin")
	cfg := Config{BaseURL: u}
	wsURL := cfg.WebSocketURL()
	assert.True(t, strings.HasSuffix(wsURL, "/api/v1/runner/ws"))
	assert.False(t, strings.Contains(wsURL, "malicious"))
}

// TestSnapshotForSyncFoldsInCloudServices verifies the tasks.sync snapshot
// includes daemon-supervised services the task manager holds (notably
// cloud-declared "cloud-<id>" services registered at runtime via service:apply),
// which the TOML localTasks set does not carry. Run-to-completion tasks the
// manager holds (e.g. ad-hoc inline cloud runs) are not folded in.
func TestSnapshotForSyncFoldsInCloudServices(t *testing.T) {
	env := newTestEnv(t, func(*websocket.Conn) {})
	defer env.close()

	env.taskManager.UpsertTask(&model.Task{
		Name:           "cloud-01HZX",
		Kind:           model.KindService,
		Run:            "echo hi",
		Restart:        model.RestartAlways,
		MaxConcurrent:  1,
		OnOverlap:      model.PolicySkip,
		Instances:      1,
		RestartDelay:   time.Millisecond,
		RestartBackoff: model.BackoffConstant,
	})
	// A non-service task the manager also holds must not be folded in.
	env.taskManager.UpsertTask(&model.Task{Name: "adhoc-run", Run: "echo x", MaxConcurrent: 1})

	snapshot := env.client.snapshotForSync()

	if _, ok := snapshot["cloud-01HZX"]; !ok {
		t.Fatalf("snapshot missing cloud service: %v", snapshotNames(snapshot))
	}
	if _, ok := snapshot["adhoc-run"]; ok {
		t.Fatalf("snapshot must not include non-service tasks: %v", snapshotNames(snapshot))
	}
}

func snapshotNames(m map[string]*model.Task) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	return names
}

// testTaskRunnerAdapter mirrors the cmd/runwisp cloudTaskRunner adapter for
// the cloud package's own tests, mapping cloud.TaskRunner.TriggerCloudRun
// onto runtime.TaskManager.TriggerRunWithOptions. Kept minimal — production
// wiring lives in apps/runwisp/cmd/runwisp/cloud_adapters.go.
type testTaskRunnerAdapter struct {
	inner runtime.TaskManager
}

func (a *testTaskRunnerAdapter) GetTask(name string) (*model.Task, bool) {
	return a.inner.GetTask(name)
}

func (a *testTaskRunnerAdapter) ListServiceTasks() []*model.Task {
	return a.inner.ListServiceTasks()
}

func (a *testTaskRunnerAdapter) UpsertTask(task *model.Task) {
	a.inner.UpsertTask(task)
}

func (a *testTaskRunnerAdapter) RemoveTask(taskName string) {
	a.inner.RemoveTask(taskName)
}

func (a *testTaskRunnerAdapter) TriggerCloudRun(taskName, executionID string, params map[string]string) (*model.Run, error) {
	return a.inner.TriggerRunWithOptions(taskName, runtime.TriggerRunOptions{
		TriggeredBy: model.TriggeredByCloud,
		ExecutionID: executionID,
		Params:      model.PointerValues(params),
	})
}

func (a *testTaskRunnerAdapter) TerminateRunByExecutionID(executionID string) error {
	return a.inner.TerminateRunByExecutionID(executionID)
}

func (a *testTaskRunnerAdapter) StartServiceInstances(taskName string, triggeredBy model.TriggeredBy) error {
	return a.inner.StartServiceInstances(taskName, triggeredBy)
}

func (a *testTaskRunnerAdapter) StopService(taskName string) error {
	return a.inner.StopService(taskName)
}

func (a *testTaskRunnerAdapter) RestartServiceInstances(taskName string) error {
	return a.inner.RestartServiceInstances(taskName)
}

func (a *testTaskRunnerAdapter) ServiceSnapshot(taskName string) (model.ServiceSnapshot, bool) {
	return a.inner.ServiceSnapshot(taskName)
}
