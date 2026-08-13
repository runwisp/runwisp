// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/runwisp/runwisp/internal/server/logstream"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/runwisp/runwisp/internal/tui/views/execlist"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newNilClientSM returns a StreamManager whose underlying client is nil; the
// command-returning methods all early-return nil on this branch so the tests
// don't need to spin up a real HTTP client.
func newNilClientSM() StreamManager { return NewStreamManager(nil) }

func TestStreamManager_ShutdownCancelsContext(t *testing.T) {
	sm := newNilClientSM()
	require.NoError(t, sm.streamCtx.Err(), "fresh ctx is alive")
	sm.Shutdown()
	assert.Error(t, sm.streamCtx.Err(), "ctx cancelled after Shutdown")
}

func TestStreamManager_NilClientReturnsNilCommands(t *testing.T) {
	sm := newNilClientSM()
	t.Cleanup(sm.Shutdown)

	// Each of these short-circuits to nil when the client is nil. We exercise
	// every method to lock in that contract.
	assert.Nil(t, sm.StartLogStream(&model.Run{ID: "r"}, 0))
	assert.Nil(t, sm.FetchOlderLogs("t", "r", 100, 50))
	assert.Nil(t, sm.FetchSystemStats())
	assert.Nil(t, sm.FetchRunSummary())
	assert.Nil(t, sm.FetchMetricsHistory())
	assert.Nil(t, sm.SubscribeDaemonLogs())
	assert.Nil(t, sm.SubscribeNotifications())
	assert.Nil(t, sm.FetchUnreadCount())
	assert.Nil(t, sm.FetchNotifications())
	assert.Nil(t, sm.MarkNotificationRead(""))
	assert.Nil(t, sm.MarkNotificationRead("id"))
	assert.Nil(t, sm.MarkNotificationUnread(""))
	assert.Nil(t, sm.MarkNotificationUnread("id"))
}

func TestStreamManager_SubscribeEventsNilReturnsNil(t *testing.T) {
	sm := newNilClientSM()
	t.Cleanup(sm.Shutdown)

	cmd := sm.SubscribeEvents()
	require.NotNil(t, cmd, "non-nil tea.Cmd is returned even with nil client")
	assert.Nil(t, cmd(), "running the cmd returns nil tea.Msg for a nil client")
}

func TestStreamManager_FetchOlderLogs_LimitZeroReturnsNil(t *testing.T) {
	// Use a non-nil client so we get past the nil-client guard but exit at the
	// limit guard. NewStreamManager initialises the context so Shutdown is safe.
	sm := NewStreamManager(&apiclient.Client{})
	t.Cleanup(sm.Shutdown)
	// beforeLine == 0 → startLine == 0 → limit == 0 → cmd is nil.
	assert.Nil(t, sm.FetchOlderLogs("t", "r", 0, 50))
}

func TestStreamManager_ContinueListeningSSE(t *testing.T) {
	sm := newNilClientSM()
	t.Cleanup(sm.Shutdown)
	assert.Nil(t, sm.ContinueListeningSSE(), "no stored channel → nil")

	ch := make(chan apiclient.RunStreamEvent, 1)
	cmd := sm.OnSSEConnected(ch)
	require.NotNil(t, cmd)
	assert.NotNil(t, sm.ContinueListeningSSE(), "channel stored → non-nil cmd")
}

func TestStreamManager_ContinueListeningLog(t *testing.T) {
	sm := newNilClientSM()
	t.Cleanup(sm.Shutdown)
	assert.Nil(t, sm.ContinueListeningLog("r"))

	ch := make(chan apiclient.LogStreamMsg, 1)
	cmd := sm.OnLogConnected("r", ch)
	require.NotNil(t, cmd)
	assert.NotNil(t, sm.ContinueListeningLog("r"))
}

func TestStreamManager_FetchOlderLogs_RunsAndReturnsLoadedMsg(t *testing.T) {
	// Spin a fake daemon that serves a single log page; FetchOlderLogs should
	// translate it into a LogOlderLoadedMsg.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"lines":[{"n":5,"text":"hi","stream":"stdout"}],"totalLines":42}`))
	}))
	defer srv.Close()

	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	cmd := sm.FetchOlderLogs("task", "run", 10, 5)
	require.NotNil(t, cmd)
	msg := cmd()
	loaded, ok := msg.(uikit.LogOlderLoadedMsg)
	require.True(t, ok, "expected LogOlderLoadedMsg, got %T", msg)
	assert.Equal(t, "run", loaded.RunID)
	assert.EqualValues(t, 42, loaded.Total)
	require.Len(t, loaded.Lines, 1)
	assert.EqualValues(t, 5, loaded.FirstLine)
}

func TestStreamManager_FetchOlderLogs_ServerErrorReturnsDebugMsg(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	cmd := sm.FetchOlderLogs("task", "run", 10, 5)
	require.NotNil(t, cmd)
	msg := cmd()
	dbg, ok := msg.(uikit.DebugLogMsg)
	require.True(t, ok, "expected DebugLogMsg on server error, got %T", msg)
	assert.Contains(t, dbg.Message, "Failed to load older logs")
}

func TestStreamManager_CancelLogStream_Idempotent(t *testing.T) {
	sm := newNilClientSM()
	t.Cleanup(sm.Shutdown)
	// Cancelling twice (no stream open) is a no-op.
	sm.CancelLogStream()
	sm.CancelLogStream()
}

func TestStreamManager_CancelLogStreamResetsChannel(t *testing.T) {
	sm := newNilClientSM()
	t.Cleanup(sm.Shutdown)

	ch := make(chan apiclient.LogStreamMsg, 1)
	sm.OnLogConnected("r", ch)
	assert.NotNil(t, sm.ContinueListeningLog("r"))

	sm.CancelLogStream()
	assert.Nil(t, sm.ContinueListeningLog("r"), "cancelled → nil cmd")
}

func TestStreamManager_ContinueListeningDaemonLog(t *testing.T) {
	sm := newNilClientSM()
	t.Cleanup(sm.Shutdown)
	assert.Nil(t, sm.ContinueListeningDaemonLog())

	ch := make(chan string, 1)
	cmd := sm.OnDaemonLogConnected(ch)
	require.NotNil(t, cmd)
	assert.NotNil(t, sm.ContinueListeningDaemonLog())
}

func TestStreamManager_ContinueListeningNotifications(t *testing.T) {
	sm := newNilClientSM()
	t.Cleanup(sm.Shutdown)
	assert.Nil(t, sm.ContinueListeningNotifications())

	ch := make(chan apiclient.NotificationStreamEvent, 1)
	cmd := sm.OnNotificationConnected(ch)
	require.NotNil(t, cmd)
	assert.NotNil(t, sm.ContinueListeningNotifications())
}

func TestListenChannel_DeliversValue(t *testing.T) {
	ch := make(chan int, 1)
	ch <- 42

	cmd := listenChannel(ch, func(v int) tea.Msg { return v }, tea.Msg("closed"))
	msg := cmd()
	assert.Equal(t, 42, msg)
}

func TestListenChannel_ReturnsOnClosedSentinel(t *testing.T) {
	ch := make(chan int)
	close(ch)

	cmd := listenChannel(ch, func(int) tea.Msg { return "value" }, tea.Msg("closed"))
	msg := cmd()
	assert.Equal(t, tea.Msg("closed"), msg)
}

func TestListenSSE_MapsToSSEEventMsg(t *testing.T) {
	ch := make(chan apiclient.RunStreamEvent, 1)
	ch <- apiclient.RunStreamEvent{Type: "run.created"}

	cmd := listenSSE(ch)
	msg := cmd()
	got, ok := msg.(uikit.SSEEventMsg)
	require.True(t, ok)
	assert.Equal(t, "run.created", got.Event.Type)
}

func TestListenSSE_ClosedChannelReturnsDisconnectedSentinel(t *testing.T) {
	ch := make(chan apiclient.RunStreamEvent)
	close(ch)
	cmd := listenSSE(ch)
	msg := cmd()
	_, ok := msg.(uikit.SSEDisconnectedMsg)
	assert.True(t, ok)
}

func TestListenLogStream_KindLine(t *testing.T) {
	ch := make(chan apiclient.LogStreamMsg, 1)
	ch <- apiclient.LogStreamMsg{
		Kind: apiclient.LogStreamMsgKindLine,
		Line: server.LogLineEntry{N: 1, Text: "hello"},
	}
	msg := listenLogStream("rid", ch)()
	got, ok := msg.(uikit.LogLineMsg)
	require.True(t, ok)
	assert.Equal(t, "rid", got.RunID)
	assert.Equal(t, "hello", got.Line.Text)
}

func TestListenLogStream_KindRotated(t *testing.T) {
	ch := make(chan apiclient.LogStreamMsg, 1)
	ch <- apiclient.LogStreamMsg{
		Kind:    apiclient.LogStreamMsgKindRotated,
		Rotated: logstream.RotatedEvent{FirstAvailable: 100},
	}
	msg := listenLogStream("rid", ch)()
	got, ok := msg.(uikit.LogRotatedMsg)
	require.True(t, ok)
	assert.EqualValues(t, 100, got.FirstAvailable)
}

func TestListenLogStream_KindDropped(t *testing.T) {
	ch := make(chan apiclient.LogStreamMsg, 1)
	ch <- apiclient.LogStreamMsg{
		Kind:    apiclient.LogStreamMsgKindDropped,
		Dropped: logstream.DroppedEvent{After: 5, Count: 7},
	}
	msg := listenLogStream("rid", ch)()
	got, ok := msg.(uikit.LogDroppedMsg)
	require.True(t, ok)
	assert.EqualValues(t, 5, got.After)
	assert.EqualValues(t, 7, got.Count)
}

func TestListenLogStream_KindDone(t *testing.T) {
	ch := make(chan apiclient.LogStreamMsg, 1)
	ch <- apiclient.LogStreamMsg{
		Kind: apiclient.LogStreamMsgKindDone,
		Done: logstream.DoneEvent{FinalLine: 99},
	}
	msg := listenLogStream("rid", ch)()
	got, ok := msg.(uikit.LogDoneMsg)
	require.True(t, ok)
	assert.EqualValues(t, 99, got.FinalLine)
}

func TestListenLogStream_KindErr(t *testing.T) {
	ch := make(chan apiclient.LogStreamMsg, 1)
	ch <- apiclient.LogStreamMsg{Kind: apiclient.LogStreamMsgKindErr}
	msg := listenLogStream("rid", ch)()
	got, ok := msg.(uikit.LogDoneMsg)
	require.True(t, ok)
	assert.Equal(t, "rid", got.RunID)
}

func TestListenLogStream_ClosedChannelReturnsDone(t *testing.T) {
	ch := make(chan apiclient.LogStreamMsg)
	close(ch)
	msg := listenLogStream("rid", ch)()
	got, ok := msg.(uikit.LogDoneMsg)
	require.True(t, ok)
	assert.Equal(t, "rid", got.RunID)
}

func TestListenNotifications_DeliversEvent(t *testing.T) {
	ch := make(chan apiclient.NotificationStreamEvent, 1)
	ch <- apiclient.NotificationStreamEvent{Type: "created"}
	msg := listenNotifications(ch)()
	got, ok := msg.(uikit.NotificationEventMsg)
	require.True(t, ok)
	assert.Equal(t, "created", got.Event.Type)
}

func TestListenNotifications_ClosedSentinel(t *testing.T) {
	ch := make(chan apiclient.NotificationStreamEvent)
	close(ch)
	msg := listenNotifications(ch)()
	_, ok := msg.(uikit.NotificationStreamDisconnectedMsg)
	assert.True(t, ok)
}

func TestListenDaemonLog_DeliversLine(t *testing.T) {
	ch := make(chan string, 1)
	ch <- "raw line"
	msg := listenDaemonLog(ch)()
	got, ok := msg.(uikit.DaemonLogLineMsg)
	require.True(t, ok)
	assert.Equal(t, "raw line", got.Line)
}

func TestListenDaemonLog_ClosedSentinel(t *testing.T) {
	ch := make(chan string)
	close(ch)
	msg := listenDaemonLog(ch)()
	_, ok := msg.(uikit.DaemonLogDisconnectedMsg)
	assert.True(t, ok)
}

// ─── Non-nil-client fetch helpers ────────────────────────────────────────────

// newFakeDaemonServer returns an httptest server that responds with the
// minimum valid JSON envelope for the endpoints the StreamManager fetches.
// This unlocks the non-nil-client branches of FetchSystemStats / Run Summary
// / Metrics History / Unread Count / Notifications / Mark*.
func newFakeDaemonServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/system", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/api/system/history", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	})
	mux.HandleFunc("/api/runs/summary", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/api/notifications", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[]}`))
	})
	mux.HandleFunc("/api/notifications/unread-count", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"unread_count":0}`))
	})
	// Read/unread state changes return an updated NotificationDTO body.
	mux.HandleFunc("/api/notifications/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"id"}`))
	})
	return httptest.NewServer(mux)
}

func TestStreamManager_FetchSystemStats_HappyPath(t *testing.T) {
	srv := newFakeDaemonServer(t)
	defer srv.Close()
	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	cmd := sm.FetchSystemStats()
	require.NotNil(t, cmd)
	msg, ok := cmd().(uikit.SystemStatsMsg)
	require.True(t, ok)
	assert.NoError(t, msg.Err)
	assert.NotNil(t, msg.Stats)
}

func TestStreamManager_FetchRunSummary_HappyPath(t *testing.T) {
	srv := newFakeDaemonServer(t)
	defer srv.Close()
	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	cmd := sm.FetchRunSummary()
	require.NotNil(t, cmd)
	msg, ok := cmd().(uikit.RunSummaryMsg)
	require.True(t, ok)
	assert.NoError(t, msg.Err)
}

func TestStreamManager_FetchMetricsHistory_HappyPath(t *testing.T) {
	srv := newFakeDaemonServer(t)
	defer srv.Close()
	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	cmd := sm.FetchMetricsHistory()
	require.NotNil(t, cmd)
	msg, ok := cmd().(uikit.MetricsHistoryMsg)
	require.True(t, ok)
	assert.NoError(t, msg.Err)
}

func TestStreamManager_FetchUnreadCount_HappyPath(t *testing.T) {
	srv := newFakeDaemonServer(t)
	defer srv.Close()
	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	cmd := sm.FetchUnreadCount()
	require.NotNil(t, cmd)
	msg, ok := cmd().(uikit.NotificationUnreadCountMsg)
	require.True(t, ok)
	assert.NoError(t, msg.Err)
}

func TestStreamManager_FetchNotifications_HappyPath(t *testing.T) {
	srv := newFakeDaemonServer(t)
	defer srv.Close()
	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	cmd := sm.FetchNotifications()
	require.NotNil(t, cmd)
	msg, ok := cmd().(uikit.NotificationsLoadedMsg)
	require.True(t, ok)
	assert.NoError(t, msg.Err)
}

func TestStreamManager_MarkNotificationRead_HappyPath(t *testing.T) {
	srv := newFakeDaemonServer(t)
	defer srv.Close()
	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	cmd := sm.MarkNotificationRead("id")
	require.NotNil(t, cmd)
	msg, ok := cmd().(uikit.NotificationReadStateMsg)
	require.True(t, ok)
	assert.Equal(t, "id", msg.ID)
	assert.True(t, msg.Read)
}

func TestStreamManager_MarkNotificationUnread_HappyPath(t *testing.T) {
	srv := newFakeDaemonServer(t)
	defer srv.Close()
	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	cmd := sm.MarkNotificationUnread("id")
	require.NotNil(t, cmd)
	msg, ok := cmd().(uikit.NotificationReadStateMsg)
	require.True(t, ok)
	assert.Equal(t, "id", msg.ID)
	assert.False(t, msg.Read)
}

// ─── SubscribeEvents / Daemon logs / Notifications: connected path ──────────

func TestStreamManager_SubscribeEvents_ConnectedReturnsChannel(t *testing.T) {
	// SSE handler that immediately closes the connection so the consumer
	// goroutine exits without leaking.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// no body — connection closes
	}))
	defer srv.Close()
	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	cmd := sm.SubscribeEvents()
	require.NotNil(t, cmd)
	msg := cmd()
	_, ok := msg.(uikit.SSEConnectedMsg)
	assert.True(t, ok, "expected SSEConnectedMsg, got %T", msg)
}

func TestStreamManager_SubscribeDaemonLogs_ConnectedReturnsChannel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	defer srv.Close()
	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	cmd := sm.SubscribeDaemonLogs()
	require.NotNil(t, cmd)
	msg := cmd()
	_, ok := msg.(uikit.DaemonLogConnectedMsg)
	assert.True(t, ok, "expected DaemonLogConnectedMsg, got %T", msg)
}

func TestStreamManager_SubscribeNotifications_ConnectedReturnsChannel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	defer srv.Close()
	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	cmd := sm.SubscribeNotifications()
	require.NotNil(t, cmd)
	msg := cmd()
	_, ok := msg.(uikit.NotificationStreamConnectedMsg)
	assert.True(t, ok, "expected NotificationStreamConnectedMsg, got %T", msg)
}

func TestStreamManager_StartLogStream_ConnectErrorReturnsDoneMsg(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	cmd := sm.StartLogStream(&model.Run{ID: "r1", TaskName: "task"}, 0)
	require.NotNil(t, cmd)
	msg := cmd()
	got, ok := msg.(uikit.LogDoneMsg)
	require.True(t, ok, "expected LogDoneMsg, got %T", msg)
	assert.Equal(t, "r1", got.RunID)
}

func TestStreamManager_StartLogStream_ConnectedReturnsLogStreamConnectedMsg(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	defer srv.Close()
	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	cmd := sm.StartLogStream(&model.Run{ID: "r2", TaskName: "task"}, 0)
	require.NotNil(t, cmd)
	msg := cmd()
	got, ok := msg.(uikit.LogStreamConnectedMsg)
	require.True(t, ok, "expected LogStreamConnectedMsg, got %T", msg)
	assert.Equal(t, "r2", got.RunID)
}

func TestStreamManager_StartLogStream_CancelsPreviousStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
	}))
	defer srv.Close()
	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	// First stream owns logCancel.
	_ = sm.StartLogStream(&model.Run{ID: "r1", TaskName: "t"}, 0)
	require.NotNil(t, sm.logCancel)

	// Second stream cancels the first and installs its own cancel.
	_ = sm.StartLogStream(&model.Run{ID: "r2", TaskName: "t"}, 0)
	require.NotNil(t, sm.logCancel)
}

func TestStreamManager_CancelLogStream_NoOpWhenIdle(t *testing.T) {
	sm := newNilClientSM()
	t.Cleanup(sm.Shutdown)

	// No active stream — Cancel must be a no-op (not panic, no state set).
	sm.CancelLogStream()
	assert.Nil(t, sm.logCancel)
	assert.Nil(t, sm.logCh)
}

func TestStreamManager_FetchExecWindow_NilFnReturnsNilWhenLoading(t *testing.T) {
	sm := newNilClientSM()
	t.Cleanup(sm.Shutdown)

	// First call seeds w.loading=true; second call hits the early-return path.
	w := &execlist.ExecWindow{}
	first := sm.FetchExecWindow(w, 0, 10)
	require.NotNil(t, first, "first call returns a fetch cmd")

	second := sm.FetchExecWindow(w, 0, 10)
	assert.Nil(t, second, "concurrent call must short-circuit while loading")
}

// newBulkServer answers the bulk/reload/info/runs/history endpoints the
// remaining StreamManager commands hit, with minimal valid JSON envelopes.
func newBulkServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	bulkAffected := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"affected":3}`))
	}
	mux.HandleFunc("/api/runs/bulk/delete", bulkAffected)
	mux.HandleFunc("/api/runs/bulk/restore", bulkAffected)
	mux.HandleFunc("/api/runs/bulk/stop", bulkAffected)
	mux.HandleFunc("/api/runs/bulk/rerun", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"triggered":[{"task_name":"t","run_id":"r"}]}`))
	})
	mux.HandleFunc("/api/notifications/read", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/info", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	})
	mux.HandleFunc("/api/reload", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"added":["new-task"],"removed":[],"changed":[]}`))
	})
	mux.HandleFunc("/api/runs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"runs":[],"total":0}`))
	})
	// Per-task runs (FetchTaskSummary) and log-line history share the /api/tasks/ prefix.
	mux.HandleFunc("/api/tasks/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/log/line/") {
			_, _ = w.Write([]byte(`{"frames":[["frame-1"],["frame-2"]]}`))
			return
		}
		_, _ = w.Write([]byte(`{"runs":[],"total":0}`))
	})
	return httptest.NewServer(mux)
}

func TestStreamManager_BulkActions_HappyPath(t *testing.T) {
	srv := newBulkServer(t)
	defer srv.Close()
	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	sel := model.RunSelector{IDs: []string{"r1", "r2"}}
	cases := []struct {
		name     string
		cmd      tea.Cmd
		verb     string
		affected int
	}{
		{"restore", sm.RestoreRuns(sel), "Restored", 3},
		{"cancel", sm.CancelRuns(sel), "Cancelled", 3},
		// Rerun reports the number of runs it spawned, not the affected count.
		{"rerun", sm.RerunRuns(sel), "Reran", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NotNil(t, tc.cmd)
			msg, ok := tc.cmd().(uikit.BulkActionMsg)
			require.True(t, ok)
			assert.NoError(t, msg.Err)
			assert.Equal(t, tc.verb, msg.Action)
			assert.Equal(t, tc.affected, msg.Affected)
		})
	}
}

func TestStreamManager_DeleteRunsUndoable_HappyPath(t *testing.T) {
	srv := newBulkServer(t)
	defer srv.Close()
	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	sel := model.RunSelector{IDs: []string{"r1"}}
	cmd := sm.DeleteRunsUndoable(sel)
	require.NotNil(t, cmd)
	msg, ok := cmd().(uikit.BulkDeleteResultMsg)
	require.True(t, ok)
	assert.NoError(t, msg.Err)
	assert.Equal(t, 3, msg.Affected)
	assert.Equal(t, sel, msg.Restore, "the selector rides along so an undo can restore it")
}

func TestStreamManager_MarkAllNotificationsRead_HappyPath(t *testing.T) {
	srv := newBulkServer(t)
	defer srv.Close()
	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	cmd := sm.MarkAllNotificationsRead()
	require.NotNil(t, cmd)
	assert.Nil(t, cmd(), "a successful mark-all returns no message")
}

func TestStreamManager_FetchDaemonInfo_HappyPath(t *testing.T) {
	srv := newBulkServer(t)
	defer srv.Close()
	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	cmd := sm.FetchDaemonInfo()
	require.NotNil(t, cmd)
	msg, ok := cmd().(uikit.DaemonInfoMsg)
	require.True(t, ok)
	assert.NoError(t, msg.Err)
}

func TestStreamManager_Reload_HappyPath(t *testing.T) {
	srv := newBulkServer(t)
	defer srv.Close()
	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	cmd := sm.Reload()
	require.NotNil(t, cmd)
	msg, ok := cmd().(uikit.ReloadResultMsg)
	require.True(t, ok)
	assert.NoError(t, msg.Err)
	require.NotNil(t, msg.Result)
	assert.Equal(t, []string{"new-task"}, msg.Result.Added)
	// The follow-up /api/info read succeeds, so fresh info rides along.
	assert.NotNil(t, msg.Info)
}

func TestStreamManager_FetchTaskSummary_HappyPath(t *testing.T) {
	srv := newBulkServer(t)
	defer srv.Close()
	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	assert.Nil(t, sm.FetchTaskSummary(""), "empty task name short-circuits")

	cmd := sm.FetchTaskSummary("alpha")
	require.NotNil(t, cmd)
	msg, ok := cmd().(uikit.TaskSummaryMsg)
	require.True(t, ok)
	assert.NoError(t, msg.Err)
	assert.Equal(t, "alpha", msg.TaskName)
}

func TestStreamManager_FetchLineHistory_HappyPath(t *testing.T) {
	srv := newBulkServer(t)
	defer srv.Close()
	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	cmd := sm.FetchLineHistory("alpha", "r1", 7, "committed text")
	require.NotNil(t, cmd)
	msg, ok := cmd().(uikit.LogLineHistoryMsg)
	require.True(t, ok)
	assert.NoError(t, msg.Err)
	assert.Equal(t, int64(7), msg.Line)
	assert.Equal(t, "committed text", msg.Committed)
	assert.Len(t, msg.Frames, 2)
}

func TestStreamManager_FetchExecWindow_HappyPath(t *testing.T) {
	srv := newBulkServer(t)
	defer srv.Close()
	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	w := execlist.NewExecWindow(apiclient.New(srv.URL, ""))
	cmd := sm.FetchExecWindow(w, 0, 10)
	require.NotNil(t, cmd)
	msg, ok := cmd().(uikit.ExecWindowFetchedMsg)
	require.True(t, ok, "a successful fetch yields ExecWindowFetchedMsg")
	assert.Equal(t, 0, msg.Total)
}

func TestStreamManager_FetchExecWindow_ServerErrorReturnsDebugMsg(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	sm := NewStreamManager(apiclient.New(srv.URL, ""))
	t.Cleanup(sm.Shutdown)

	w := execlist.NewExecWindow(apiclient.New(srv.URL, ""))
	cmd := sm.FetchExecWindow(w, 0, 10)
	require.NotNil(t, cmd)
	_, ok := cmd().(uikit.DebugLogMsg)
	assert.True(t, ok, "a fetch error surfaces as a DebugLogMsg")
}
