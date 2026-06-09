// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
// fresh data dir and points flags.DataDir at it, so runStatus() dials the mux
// over the local-trusted transport exactly as it would a live daemon.
func serveStatusSocket(t *testing.T, mux http.Handler) {
	t.Helper()
	// ShortTempDir keeps DataDir (and thus runwisp.sock) under macOS' 104-byte
	// sun_path limit so net.Listen("unix", ...) can actually bind.
	dir := testutil.ShortTempDir(t)
	orig := flags.DataDir
	flags.DataDir = dir
	t.Cleanup(func() { flags.DataDir = orig })

	ln, err := net.Listen("unix", localAPISocketPath())
	require.NoError(t, err)

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
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
	serveStatusSocket(t, mux)

	var buf bytes.Buffer
	require.NoError(t, runStatus(&buf))
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
	serveStatusSocket(t, mux)

	var buf bytes.Buffer
	require.NoError(t, runStatus(&buf))
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
	serveStatusSocket(t, mux)

	var buf bytes.Buffer
	require.NoError(t, runStatus(&buf))
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
	serveStatusSocket(t, mux)

	var buf bytes.Buffer
	require.NoError(t, runStatus(&buf))
	assert.Contains(t, buf.String(), "runwisp.toml has changed")
}

func TestRunStatus_HealthNon200Errors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	serveStatusSocket(t, mux)

	var buf bytes.Buffer
	err := runStatus(&buf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not reachable")
}

func TestRunStatus_UnreachableErrors(t *testing.T) {
	dir := testutil.ShortTempDir(t)
	orig := flags.DataDir
	flags.DataDir = dir
	t.Cleanup(func() { flags.DataDir = orig })

	// No socket created — HealthCheck fails to dial.
	var buf bytes.Buffer
	err := runStatus(&buf)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not reachable")
}
