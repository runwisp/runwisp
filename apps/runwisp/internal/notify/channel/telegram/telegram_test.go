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
	"github.com/runwisp/runwisp/internal/notify/render"
)

func newTestRenderer(t *testing.T) render.Renderer {
	t.Helper()
	body, err := render.LoadDefaultTemplate("telegram")
	require.NoError(t, err)
	r, err := render.NewTemplateRenderer("telegram:test", body, "text/html", render.DefaultTitle)
	require.NoError(t, err)
	return r
}

func newFastTransport() *notify.HTTPProvider {
	return &notify.HTTPProvider{
		Client:    &http.Client{Timeout: 2 * time.Second},
		Backoff:   notify.BackoffConfig{InitialInterval: 5 * time.Millisecond, MaxInterval: 20 * time.Millisecond, MaxElapsedTime: 100 * time.Millisecond, Multiplier: 2.0},
		Body429Fn: parseTelegramRetryAfter,
		UserAgent: "runwisp-notify/test",
	}
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
		Renderer: newTestRenderer(t), APIBase: srv.URL,
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
		Renderer: newTestRenderer(t), APIBase: srv.URL,
		Transport: newFastTransport(),
	})
	require.NoError(t, err)
	require.NoError(t, ch.Execute(context.Background(), &notify.Event{
		Kind: notify.KindRunFailed, Severity: notify.SevError, TaskName: "x",
	}))
	assert.EqualValues(t, 2, hits.Load())
}

func TestTelegram_RedactsTokenInError(t *testing.T) {
	ch, err := New(Config{
		ID: "oncall", BotToken: "TOPSECRET:Z", ChatID: "-1001",
		Renderer: newTestRenderer(t), APIBase: "http://127.0.0.1:1",
		Transport: newFastTransport(),
	})
	require.NoError(t, err)
	err = ch.Execute(context.Background(), &notify.Event{Kind: notify.KindRunFailed, Severity: notify.SevError})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "TOPSECRET", "bot token must not leak")
	assert.True(t, strings.HasPrefix(err.Error(), "telegram:oncall:"), "error prefix must be the redacted id")
}
