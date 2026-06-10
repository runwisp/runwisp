// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/testutil"
)

// newFastTransport layers telegram's 429 Retry-After parser on top of the
// shared fast test transport.
func newFastTransport() *notify.HTTPProvider {
	tr := testutil.NewFastTransport()
	tr.Body429Fn = parseTelegramRetryAfter
	return tr
}

func TestTelegram_PostsForm(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.True(t, strings.HasPrefix(r.URL.Path, "/bot"))
		require.NoError(t, r.ParseForm())
		assert.Equal(t, "-1001", r.Form.Get("chat_id"))
		assert.Equal(t, "HTML", r.Form.Get("parse_mode"))
		assert.NotEmpty(t, r.Form.Get("text"))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch, err := New(Config{
		ID: "oncall", BotToken: "abc:xyz", ChatID: "-1001",
		Renderer: testutil.NewTestRenderer(t, "telegram", "text/html"), APIBase: srv.URL,
		Transport: newFastTransport(),
	})
	require.NoError(t, err)
	require.NoError(t, ch.Execute(context.Background(), &notify.Event{
		Kind: notify.KindRunFailed, Severity: notify.SevError,
		TaskName: "backup-db", Reason: "exit 1",
		Timestamp: time.Now().UTC(),
	}))
}

func TestTelegram_HonorsBodyRetryAfter(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if hits.Load() == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"ok":false,"parameters":{"retry_after":0}}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch, err := New(Config{
		ID: "oncall", BotToken: "abc:xyz", ChatID: "-1001",
		Renderer: testutil.NewTestRenderer(t, "telegram", "text/html"), APIBase: srv.URL,
		Transport: newFastTransport(),
	})
	require.NoError(t, err)
	require.NoError(t, ch.Execute(context.Background(), &notify.Event{
		Kind: notify.KindRunFailed, Severity: notify.SevError, TaskName: "x",
	}))
	assert.EqualValues(t, 2, hits.Load())
}

func TestTelegram_SendsRenderedBody(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		seen = r.Form.Get("text")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch, err := New(Config{
		ID: "oncall", BotToken: "abc:xyz", ChatID: "-1001",
		Renderer: testutil.NewTestRenderer(t, "telegram", "text/html"), APIBase: srv.URL,
		Transport: newFastTransport(),
	})
	require.NoError(t, err)
	require.NoError(t, ch.Execute(context.Background(), &notify.Event{
		Kind: notify.KindRunFailed, Severity: notify.SevError,
		TaskName: "backup-db", Reason: "exit 1",
		Timestamp: time.Now().UTC(),
	}))
	assert.Contains(t, seen, "<b>backup-db</b> failed", "rendered body should carry the headline")
	assert.Contains(t, seen, "<i>from runwisp</i>", "footer is always present, fingerprint omitted when blank")
}

func TestTelegram_RedactsTokenInError(t *testing.T) {
	ch, err := New(Config{
		ID: "oncall", BotToken: "TOPSECRET:Z", ChatID: "-1001",
		Renderer: testutil.NewTestRenderer(t, "telegram", "text/html"), APIBase: "http://127.0.0.1:1",
		Transport: newFastTransport(),
	})
	require.NoError(t, err)
	err = ch.Execute(context.Background(), &notify.Event{Kind: notify.KindRunFailed, Severity: notify.SevError})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "TOPSECRET", "bot token must not leak")
	assert.True(t, strings.HasPrefix(err.Error(), "telegram:oncall:"), "error prefix must be the redacted id")
}

// TestTelegram_ChannelInterface pins the Channel interface contract on the
// telegram adapter: ID() returns the configured id, Close is a no-op, and
// String() renders the "telegram:<id>" tag used in logs and error prefixes.
func TestTelegram_ChannelInterface(t *testing.T) {
	ch, err := New(Config{
		ID: "oncall", BotToken: "abc:xyz", ChatID: "-1001",
		Renderer: testutil.NewTestRenderer(t, "telegram", "text/html"), Transport: newFastTransport(),
	})
	require.NoError(t, err)
	assert.Equal(t, "oncall", ch.ID())
	assert.NoError(t, ch.Close(context.Background()))
	assert.Equal(t, "telegram:oncall", ch.String())
}
