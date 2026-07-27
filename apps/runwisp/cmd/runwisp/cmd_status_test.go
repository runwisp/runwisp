// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serveStatusSocket binds an HTTP server to the daemon's Unix socket inside a
// fresh data dir and returns Flags pointing at it, so runStatus(&buf, f) dials
// the mux over the local-trusted transport exactly as it would a live daemon.
func serveStatusSocket(t *testing.T, mux http.Handler) Flags {
	t.Helper()
	// ShortTempDir keeps DataDir (and thus runwisp.sock) under macOS' 104-byte
	// sun_path limit so net.Listen("unix", ...) can actually bind.
	f := Flags{DataDir: testutil.ShortTempDir(t)}

	ln, err := net.Listen("unix", localAPISocketPath(f))
	require.NoError(t, err)

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return f
}

func TestPrintSystemStats_AllFields(t *testing.T) {
	var buf bytes.Buffer
	printSystemStats(&buf, &model.SystemStats{
		Version:  "v0.6.0",
		Uptime:   "1h2m",
		CPUCores: 8,
		Host:     "my-vps",
	})
	out := buf.String()
	assert.Contains(t, out, "v0.6.0")
	assert.Contains(t, out, "1h2m")
	assert.Contains(t, out, "8 cores")
	assert.Contains(t, out, "my-vps")
}

func TestRunStatus_HealthyWithSystem(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(model.DaemonInfo{Port: 9477})
	})
	mux.HandleFunc("/api/system", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(model.SystemStats{
			Version: "vX", Uptime: "5s", CPUCores: 4, Host: "unit-test",
		})
	})
	f := serveStatusSocket(t, mux)

	var buf bytes.Buffer
	require.NoError(t, runStatus(&buf, f, false))
	out := buf.String()
	assert.Contains(t, out, "RunWisp is healthy at :9477")
	assert.Contains(t, out, "vX")
	assert.Contains(t, out, "unit-test")
}

func TestRunStatus_HealthOnlySystemMissing(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(model.DaemonInfo{Port: 1234})
	})
	// /api/system unhandled → 404, so the stats block is skipped.
	f := serveStatusSocket(t, mux)

	var buf bytes.Buffer
	require.NoError(t, runStatus(&buf, f, false))
	out := buf.String()
	assert.Contains(t, out, "RunWisp is healthy")
	assert.NotContains(t, out, "Version")
}

func TestRunStatus_HealthOnlySystemBadJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(model.DaemonInfo{Port: 1234})
	})
	mux.HandleFunc("/api/system", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "not json")
	})
	f := serveStatusSocket(t, mux)

	var buf bytes.Buffer
	require.NoError(t, runStatus(&buf, f, false))
	assert.Contains(t, buf.String(), "RunWisp is healthy")
}

func TestRunStatus_ConfigStaleWarns(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/info", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(model.DaemonInfo{Port: 9477, ConfigStale: true})
	})
	f := serveStatusSocket(t, mux)

	var buf bytes.Buffer
	require.NoError(t, runStatus(&buf, f, false))
	assert.Contains(t, buf.String(), "runwisp.toml has changed")
}

func TestRunStatus_HealthNon200Errors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	f := serveStatusSocket(t, mux)

	var buf bytes.Buffer
	err := runStatus(&buf, f, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not reachable")
}

func TestRunStatus_UnreachableErrors(t *testing.T) {
	t.Parallel()
	// No socket created — HealthCheck fails to dial.
	f := Flags{DataDir: testutil.ShortTempDir(t)}
	var buf bytes.Buffer
	err := runStatus(&buf, f, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not reachable")
}
