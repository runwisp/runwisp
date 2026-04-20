// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
	eventBus    events.EventBus
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
		_, _ = w.Write([]byte(`{"result":{"data":{"success":true,"summary":{"added":0,"updated":0,"markedOutOfSync":0,"total":0}}}}`))
	}))

	baseURL, _ := url.Parse(wsServer.URL)

	bus := events.NewEventBus()
	mockExec := &testutil.MockExecutor{}
	jm := runtime.NewTaskManager(mockExec, bus)
	mockRepo := &testutil.MockRunRepository{}

	cfg := Config{
		Enabled:         true,
		BaseURL:         baseURL,
		CloudToken:      "test-token",
		AgentVersion:    "0.0.0-test",
		Fingerprint:     "test-fp",
		RequestTimeout:  5 * time.Second,
		TaskSyncTimeout: 5 * time.Second,
	}

	client, err := NewClient(cfg, Dependencies{
		TaskManager:  jm,
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

	// Wait until the client reaches Ready or Executing state
	require.Eventually(t, func() bool {
		env.client.conn.mu.Lock()
		defer env.client.conn.mu.Unlock()
		return env.client.conn.state == StateReady || env.client.conn.state == StateExecuting
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
	cancel() // cancel immediately

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

// ---------- State machine tests ----------

func TestStateTransitions(t *testing.T) {
	tests := []struct {
		name     string
		from     LifecycleState
		to       LifecycleState
		expected LifecycleState
	}{
		{"boot to connecting", StateBoot, StateConnecting, StateConnecting},
		{"connecting to authenticated", StateConnecting, StateAuthenticated, StateAuthenticated},
		{"authenticated to syncing", StateAuthenticated, StateSyncing, StateSyncing},
		{"syncing to ready", StateSyncing, StateReady, StateReady},
		{"ready to executing", StateReady, StateExecuting, StateExecuting},
		{"same state is no-op", StateReady, StateReady, StateReady},
		{"ready to reconnecting", StateReady, StateReconnecting, StateReconnecting},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newTestEnv(t, func(conn *websocket.Conn) {
				conn.Close(websocket.StatusNormalClosure, "")
			})
			defer env.close()

			env.client.conn.mu.Lock()
			env.client.conn.state = tt.from
			env.client.conn.mu.Unlock()

			env.client.conn.setState(tt.to)

			env.client.conn.mu.Lock()
			actual := env.client.conn.state
			env.client.conn.mu.Unlock()
			assert.Equal(t, tt.expected, actual)
		})
	}
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
	// Advance several times
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
	// Fill the buffer
	session.outbound <- []byte("filler")

	err := sendMessage(session, NewPingMessage())
	require.Error(t, err)
	var ce *CloudError
	require.ErrorAs(t, err, &ce)
	assert.Equal(t, CloudErrorKindTransient, ce.Kind)
	assert.Contains(t, ce.Message, "full")
}

// ---------- WebSocket URL derivation ----------

func TestWebSocketURLDerivation(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		expected string
	}{
		{"https to wss", "https://app.runwisp.com", "wss://app.runwisp.com/api/daemon/ws"},
		{"http to ws", "http://localhost:3000", "ws://localhost:3000/api/daemon/ws"},
		{"strips path", "https://app.runwisp.com/foo", "wss://app.runwisp.com/api/daemon/ws"},
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

func TestHandleInboundPayloadPong(t *testing.T) {
	env := newTestEnv(t, func(conn *websocket.Conn) {
		conn.Close(websocket.StatusNormalClosure, "")
	})
	defer env.close()

	session := &wsSession{outbound: make(chan []byte, 16)}
	payload, _ := json.Marshal(protocol.PongMessage{Type: "pong"})
	err := env.client.sessions.handleInboundPayload(session, payload)
	assert.NoError(t, err)
}

func TestHandleInboundPayloadUnsupportedType(t *testing.T) {
	env := newTestEnv(t, func(conn *websocket.Conn) {
		conn.Close(websocket.StatusNormalClosure, "")
	})
	defer env.close()

	session := &wsSession{outbound: make(chan []byte, 16)}
	payload := []byte(`{"type":"unknown:message"}`)
	err := env.client.sessions.handleInboundPayload(session, payload)
	assert.Error(t, err)
}

func TestHandleInboundPayloadInvalidJSON(t *testing.T) {
	env := newTestEnv(t, func(conn *websocket.Conn) {
		conn.Close(websocket.StatusNormalClosure, "")
	})
	defer env.close()

	session := &wsSession{outbound: make(chan []byte, 16)}
	err := env.client.sessions.handleInboundPayload(session, []byte("not json"))
	assert.Error(t, err)
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
	assert.True(t, strings.HasSuffix(wsURL, "/api/daemon/ws"))
	assert.False(t, strings.Contains(wsURL, "malicious"))
}
