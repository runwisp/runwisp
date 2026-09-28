// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package ntfy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/testutil"
)

var failed = &notify.Event{Kind: notify.KindRunFailed, Severity: notify.SevError, TaskName: "backup-db", Timestamp: time.Now().UTC()}

func TestNtfy_PublishesJSONToServerRootWithTopicAndToken(t *testing.T) {
	var path, auth string
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, auth = r.URL.Path, r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		assert.NoError(t, json.Unmarshal(b, &payload))
	}))
	defer srv.Close()

	ch, err := New(Config{
		ID: "phone", URL: srv.URL + "/", Topic: "alerts", Token: "tk_secret",
		Renderer:  testutil.NewTestRenderer(t, "ntfy", "application/json"),
		Transport: testutil.NewFastTransport(),
	})
	require.NoError(t, err)
	require.NoError(t, ch.Execute(context.Background(), failed))

	assert.Equal(t, "/", path, "JSON publishes go to the server root, not /<topic>")
	assert.Equal(t, "Bearer tk_secret", auth)
	assert.Equal(t, "alerts", payload["topic"])
	assert.Contains(t, payload["title"], "backup-db")
	assert.EqualValues(t, 4, payload["priority"])
}

func TestNtfy_NoTokenSendsNoAuthHeader(t *testing.T) {
	var sawAuth atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth.Store(r.Header.Get("Authorization") != "")
	}))
	defer srv.Close()

	ch, err := New(Config{ID: "phone", URL: srv.URL, Topic: "alerts",
		Renderer: testutil.NewTestRenderer(t, "ntfy", "application/json"), Transport: testutil.NewFastTransport()})
	require.NoError(t, err)
	require.NoError(t, ch.Execute(context.Background(), failed))
	assert.False(t, sawAuth.Load())
}

func TestNtfy_PermanentOn4xxWithoutLeakingToken(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":40301,"error":"forbidden"}`))
	}))
	defer srv.Close()

	ch, err := New(Config{ID: "phone", URL: srv.URL, Topic: "alerts", Token: "tk_secret",
		Renderer: testutil.NewTestRenderer(t, "ntfy", "application/json"), Transport: testutil.NewFastTransport()})
	require.NoError(t, err)
	err = ch.Execute(context.Background(), failed)
	require.Error(t, err)
	assert.EqualValues(t, 1, hits.Load(), "4xx must not be retried")
	assert.NotContains(t, err.Error(), "tk_secret")
	assert.Contains(t, err.Error(), "ntfy:phone:")
}
