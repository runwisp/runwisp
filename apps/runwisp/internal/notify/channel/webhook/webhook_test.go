// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package webhook

import (
	"context"
	"encoding/json"
	"io"
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

func TestWebhook_PostsJSON(t *testing.T) {
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		b, _ := io.ReadAll(r.Body)
		receivedBody = b
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch, err := New(Config{
		ID:        "my-hook",
		URL:       srv.URL,
		Renderer:  testutil.NewTestRenderer(t, "webhook", "application/json"),
		Transport: testutil.NewFastTransport(),
	})
	require.NoError(t, err)

	require.NoError(t, ch.Execute(context.Background(), &notify.Event{
		Kind: notify.KindRunFailed, Severity: notify.SevError,
		TaskName: "backup-db", Reason: "exit 1",
		Timestamp: time.Now().UTC(),
	}))

	require.NotEmpty(t, receivedBody)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(receivedBody, &payload))
	assert.Equal(t, "run.failed", payload["kind"])
	assert.Equal(t, "error", payload["severity"])
	assert.Equal(t, "backup-db", payload["task"])
}

func TestWebhook_CustomHeadersSent(t *testing.T) {
	var receivedAuth string
	var receivedCustom string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		receivedCustom = r.Header.Get("X-Custom")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch, err := New(Config{
		ID:  "my-hook",
		URL: srv.URL,
		Headers: map[string]string{
			"Authorization": "Bearer secret-token",
			"X-Custom":      "custom-value",
		},
		Renderer:  testutil.NewTestRenderer(t, "webhook", "application/json"),
		Transport: testutil.NewFastTransport(),
	})
	require.NoError(t, err)

	require.NoError(t, ch.Execute(context.Background(), &notify.Event{
		Kind: notify.KindRunFailed, Severity: notify.SevError, TaskName: "x",
		Timestamp: time.Now().UTC(),
	}))

	assert.Equal(t, "Bearer secret-token", receivedAuth)
	assert.Equal(t, "custom-value", receivedCustom)
}

func TestWebhook_NoHeadersOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch, err := New(Config{
		ID:        "my-hook",
		URL:       srv.URL,
		Renderer:  testutil.NewTestRenderer(t, "webhook", "application/json"),
		Transport: testutil.NewFastTransport(),
	})
	require.NoError(t, err)

	require.NoError(t, ch.Execute(context.Background(), &notify.Event{
		Kind: notify.KindRunSucceeded, Severity: notify.SevInfo, TaskName: "x",
		Timestamp: time.Now().UTC(),
	}))
}

func TestWebhook_RetriesOn5xxThenFails(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ch, err := New(Config{
		ID:        "my-hook",
		URL:       srv.URL,
		Renderer:  testutil.NewTestRenderer(t, "webhook", "application/json"),
		Transport: testutil.NewFastTransport(),
	})
	require.NoError(t, err)

	err = ch.Execute(context.Background(), &notify.Event{
		Kind: notify.KindRunFailed, Severity: notify.SevError, TaskName: "x",
	})
	require.Error(t, err, "must surface the transient error after backoff exhaustion")
	assert.Greater(t, int(hits.Load()), 1, "must retry at least once on 5xx")
}

func TestWebhook_PermanentOn4xxNoRetries(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	ch, err := New(Config{
		ID:        "my-hook",
		URL:       srv.URL,
		Renderer:  testutil.NewTestRenderer(t, "webhook", "application/json"),
		Transport: testutil.NewFastTransport(),
	})
	require.NoError(t, err)

	err = ch.Execute(context.Background(), &notify.Event{
		Kind: notify.KindRunFailed, Severity: notify.SevError, TaskName: "x",
	})
	require.Error(t, err)
	assert.EqualValues(t, 1, hits.Load(), "must not retry permanent 4xx")
}

func TestWebhook_RedactsURLInError(t *testing.T) {
	ch, err := New(Config{
		ID:        "my-hook",
		URL:       "http://127.0.0.1:1/hooks/B0XXXSECRET",
		Renderer:  testutil.NewTestRenderer(t, "webhook", "application/json"),
		Transport: testutil.NewFastTransport(),
	})
	require.NoError(t, err)

	err = ch.Execute(context.Background(), &notify.Event{
		Kind: notify.KindRunFailed, Severity: notify.SevError, TaskName: "x",
	})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "B0XXXSECRET", "URL must not leak into error")
	assert.True(t, strings.HasPrefix(err.Error(), "webhook:my-hook:"), "error must start with redacted channel id")
}

func TestWebhook_IDAndClose(t *testing.T) {
	ch, err := New(Config{
		ID:        "my-hook",
		URL:       "http://example.com",
		Renderer:  testutil.NewTestRenderer(t, "webhook", "application/json"),
		Transport: testutil.NewFastTransport(),
	})
	require.NoError(t, err)
	assert.Equal(t, "my-hook", ch.ID())
	assert.Equal(t, "webhook:my-hook", ch.String(), "empty Kind must default to webhook")
	assert.NoError(t, ch.Close(context.Background()))
}
