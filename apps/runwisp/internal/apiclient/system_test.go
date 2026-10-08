// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package apiclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetMetricsHistory(t *testing.T) {
	samples := []model.MetricsSample{
		{Timestamp: time.Now().Unix(), CPUUsage: 12.5},
		{Timestamp: time.Now().Unix(), CPUUsage: 13.0},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/system/metrics", r.URL.Path)
		_ = json.NewEncoder(w).Encode(server.MetricsHistoryBody{Items: samples})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	got, err := c.GetMetricsHistory(t.Context())
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestGetMetricsHistory_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	_, err := c.GetMetricsHistory(t.Context())
	assert.Error(t, err)
}

func TestGetRunSummary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/runs/summary", r.URL.Path)
		_ = json.NewEncoder(w).Encode(model.RunSummary{Total: 102, Success: 100, Failed: 2})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	got, err := c.GetRunSummary(t.Context())
	require.NoError(t, err)
	assert.Equal(t, int64(102), got.Total)
	assert.Equal(t, int64(100), got.Success)
	assert.Equal(t, int64(2), got.Failed)
}

func TestGetDaemonInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/daemon", r.URL.Path)
		_ = json.NewEncoder(w).Encode(model.DaemonInfo{Fingerprint: "fp-test"})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	got, err := c.GetDaemonInfo(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "fp-test", got.Fingerprint)
}

func TestAuthStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/auth/status", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]bool{"authRequired": true})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	got, err := c.AuthStatus(t.Context())
	require.NoError(t, err)
	assert.True(t, got.AuthRequired)
}

func TestAuthStatus_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	_, err := c.AuthStatus(t.Context())
	assert.Error(t, err)
}

func TestStreamDaemonLogs_DeliversLines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/daemon/log/stream", r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		// One valid frame, one malformed data payload (skipped), then another
		// valid frame. After the handler returns, Go closes the body, which
		// terminates the streaming loop in the client goroutine.
		fmt.Fprintln(w, "event: line")
		fmt.Fprintln(w, `data: {"line":"hello"}`)
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "event: line")
		fmt.Fprintln(w, "data: not-json")
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "event: line")
		fmt.Fprintln(w, `data: {"line":"world"}`)
		fmt.Fprintln(w, "")
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := c.StreamDaemonLogs(ctx)
	require.NoError(t, err)

	// Read the two valid lines we expect, then return — defer cancel() shuts
	// down the goroutine.
	readOne := func() string {
		select {
		case line := <-ch:
			return line
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for daemon log line")
			return ""
		}
	}
	assert.Equal(t, "hello", readOne())
	assert.Equal(t, "world", readOne())
}

func TestUnreadNotificationCount(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/notifications/unread-count", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]int{"count": 7})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	n, err := c.UnreadNotificationCount(t.Context())
	require.NoError(t, err)
	assert.EqualValues(t, 7, n)
}

func TestGetLocalCredentials_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/local/credentials", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"password":  "Kj2x9pQ7mN4vL8rT5wYz1c",
			"ephemeral": true,
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	creds, err := c.GetLocalCredentials(t.Context())
	require.NoError(t, err)
	require.NotNil(t, creds)
	assert.Equal(t, "Kj2x9pQ7mN4vL8rT5wYz1c", creds.Password)
	assert.True(t, creds.Ephemeral)
}

func TestGetLocalCredentials_404MapsToUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no shareable password", http.StatusNotFound)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	creds, err := c.GetLocalCredentials(t.Context())
	assert.Nil(t, creds)
	assert.ErrorIs(t, err, ErrLocalCredentialsUnavailable)
}

func TestGetLocalCredentials_403PropagatesAsHTTPStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not local", http.StatusForbidden)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	creds, err := c.GetLocalCredentials(t.Context())
	assert.Nil(t, creds)
	require.Error(t, err)
	assert.True(t, IsHTTPStatus(err, http.StatusForbidden),
		"403 must surface as an HTTPStatusError, not ErrLocalCredentialsUnavailable")
	assert.False(t, errors.Is(err, ErrLocalCredentialsUnavailable))
}

func TestIsHTTPStatus(t *testing.T) {
	wrapped := &HTTPStatusError{StatusCode: http.StatusNotFound, Body: "nope"}
	assert.True(t, IsHTTPStatus(wrapped, http.StatusNotFound))
	assert.False(t, IsHTTPStatus(wrapped, http.StatusForbidden))
	assert.False(t, IsHTTPStatus(errors.New("plain"), http.StatusNotFound))
}
