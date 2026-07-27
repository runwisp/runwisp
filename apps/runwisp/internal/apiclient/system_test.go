// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetMetricsHistory(t *testing.T) {
	samples := []model.MetricsSample{
		{Timestamp: time.Now().Unix(), CPUUsage: 12.5},
		{Timestamp: time.Now().Unix(), CPUUsage: 13.0},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/system/history", r.URL.Path)
		_ = json.NewEncoder(w).Encode(samples)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	got, err := c.GetMetricsHistory()
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestGetMetricsHistory_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	_, err := c.GetMetricsHistory()
	assert.Error(t, err)
}

func TestGetRunSummary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/runs/summary", r.URL.Path)
		_ = json.NewEncoder(w).Encode(model.RunSummary{Total: 102, Success: 100, Failed: 2})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	got, err := c.GetRunSummary()
	require.NoError(t, err)
	assert.Equal(t, int64(102), got.Total)
	assert.Equal(t, int64(100), got.Success)
	assert.Equal(t, int64(2), got.Failed)
}

func TestGetDaemonInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/info", r.URL.Path)
		_ = json.NewEncoder(w).Encode(model.DaemonInfo{Fingerprint: "fp-test"})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	got, err := c.GetDaemonInfo()
	require.NoError(t, err)
	assert.Equal(t, "fp-test", got.Fingerprint)
}

func TestAuthStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/auth/status", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]bool{"auth_required": true})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	got, err := c.AuthStatus()
	require.NoError(t, err)
	assert.True(t, got.AuthRequired)
}

func TestAuthStatus_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	_, err := c.AuthStatus()
	assert.Error(t, err)
}

func TestStreamDaemonLogs_DeliversLines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/daemon/log-stream", r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		// One valid line, one not-prefixed (filtered), one malformed (skipped),
		// then another valid line. After handler returns Go closes the body
		// which terminates the streaming loop in the client goroutine.
		fmt.Fprintln(w, `data: {"line":"hello"}`)
		fmt.Fprintln(w, "ignored noise")
		fmt.Fprintln(w, "data: not-json")
		fmt.Fprintln(w, `data: {"line":"world"}`)
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
	n, err := c.UnreadNotificationCount()
	require.NoError(t, err)
	assert.EqualValues(t, 7, n)
}
