// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package slack

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

func TestSlack_PostsBlockKitJSON(t *testing.T) {
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
		ID:         "ops",
		WebhookURL: srv.URL,
		Renderer:   testutil.NewTestRenderer(t, "slack", "application/json"),
		Transport:  testutil.NewFastTransport(),
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
	assert.Contains(t, payload, "blocks")
}

func TestSlack_RetriesOn5xxThenFails(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	ch, err := New(Config{
		ID:         "ops",
		WebhookURL: srv.URL,
		Renderer:   testutil.NewTestRenderer(t, "slack", "application/json"),
		Transport:  testutil.NewFastTransport(),
	})
	require.NoError(t, err)

	err = ch.Execute(context.Background(), &notify.Event{
		Kind: notify.KindRunFailed, Severity: notify.SevError, TaskName: "x",
	})
	require.Error(t, err, "must surface the transient error after backoff exhaustion")
	assert.Greater(t, int(hits.Load()), 1, "must retry at least once on 5xx")
}

func TestSlack_PermanentOn4xxNoRetries(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	ch, err := New(Config{
		ID:         "ops",
		WebhookURL: srv.URL,
		Renderer:   testutil.NewTestRenderer(t, "slack", "application/json"),
		Transport:  testutil.NewFastTransport(),
	})
	require.NoError(t, err)

	err = ch.Execute(context.Background(), &notify.Event{
		Kind: notify.KindRunFailed, Severity: notify.SevError, TaskName: "x",
	})
	require.Error(t, err)
	assert.EqualValues(t, 1, hits.Load(), "must not retry permanent 4xx")
}

func TestSlack_ChannelOverrideIncludedWhenSet(t *testing.T) {
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedBody = b
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch, err := New(Config{
		ID:         "ops",
		WebhookURL: srv.URL,
		Channel:    "#alerts",
		Renderer:   testutil.NewTestRenderer(t, "slack", "application/json"),
		Transport:  testutil.NewFastTransport(),
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
	assert.Equal(t, "#alerts", payload["channel"])
}

func TestSlack_ChannelOverrideAbsentWhenEmpty(t *testing.T) {
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedBody = b
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ch, err := New(Config{
		ID:         "ops",
		WebhookURL: srv.URL,
		Renderer:   testutil.NewTestRenderer(t, "slack", "application/json"),
		Transport:  testutil.NewFastTransport(),
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
	assert.NotContains(t, payload, "channel")
}

func TestSlack_RedactsURLInError(t *testing.T) {
	ch, err := New(Config{
		ID:         "ops",
		WebhookURL: "http://127.0.0.1:1/hooks/B0XXXSECRET",
		Renderer:   testutil.NewTestRenderer(t, "slack", "application/json"),
		Transport:  testutil.NewFastTransport(),
	})
	require.NoError(t, err)

	err = ch.Execute(context.Background(), &notify.Event{
		Kind: notify.KindRunFailed, Severity: notify.SevError, TaskName: "x",
	})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "B0XXXSECRET", "URL must not leak into error")
	assert.True(t, strings.HasPrefix(err.Error(), "slack:ops:"), "error must start with redacted channel id")
}

// TestSlack_IDAndClose pins the Channel interface contract on the slack
// adapter: ID() returns the configured id; Close is a no-op that never errs.
func TestSlack_IDAndClose(t *testing.T) {
	ch, err := New(Config{ID: "ops", WebhookURL: "http://example.com", Renderer: testutil.NewTestRenderer(t, "slack", "application/json"), Transport: testutil.NewFastTransport()})
	require.NoError(t, err)
	assert.Equal(t, "ops", ch.ID())
	assert.NoError(t, ch.Close(context.Background()))
}
