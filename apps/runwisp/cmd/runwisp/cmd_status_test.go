// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/server"
	"github.com/runwisp/runwisp/apps/runwisp/internal/testutil"
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
	mux.HandleFunc("/api/daemon", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(model.DaemonInfo{Port: 9477})
	})
	mux.HandleFunc("/api/system", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(model.SystemStats{
			Version: "vX", Uptime: "5s", CPUCores: 4, Host: "unit-test",
		})
	})
	f := serveStatusSocket(t, mux)

	var buf bytes.Buffer
	require.NoError(t, runStatus(t.Context(), &buf, f, remoteFlags{}, false))
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
	mux.HandleFunc("/api/daemon", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(model.DaemonInfo{Port: 1234})
	})
	// /api/system unhandled → 404, so the stats block is skipped.
	f := serveStatusSocket(t, mux)

	var buf bytes.Buffer
	require.NoError(t, runStatus(t.Context(), &buf, f, remoteFlags{}, false))
	out := buf.String()
	assert.Contains(t, out, "RunWisp is healthy")
	assert.NotContains(t, out, "Version")
}

func TestRunStatus_HealthOnlySystemBadJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/daemon", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(model.DaemonInfo{Port: 1234})
	})
	mux.HandleFunc("/api/system", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, "not json")
	})
	f := serveStatusSocket(t, mux)

	var buf bytes.Buffer
	require.NoError(t, runStatus(t.Context(), &buf, f, remoteFlags{}, false))
	assert.Contains(t, buf.String(), "RunWisp is healthy")
}

func TestRunStatus_ConfigStaleWarns(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/daemon", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(model.DaemonInfo{Port: 9477, ConfigStale: true})
	})
	f := serveStatusSocket(t, mux)

	var buf bytes.Buffer
	require.NoError(t, runStatus(t.Context(), &buf, f, remoteFlags{}, false))
	assert.Contains(t, buf.String(), "runwisp.toml has changed")
}

func TestRunStatus_ShowsWebUIAddressAndSource(t *testing.T) {
	cases := map[string]struct {
		info model.DaemonInfo
		want string
	}{
		"configured": {model.DaemonInfo{Port: 9477, ExternalURL: "https://rw.example.com", ExternalURLSource: "config"}, "Web UI: https://rw.example.com (external_url)"},
		"detected":   {model.DaemonInfo{Port: 9477, ExternalURL: "http://rw.lan:9477", ExternalURLSource: "detected"}, "Web UI: http://rw.lan:9477 (from the last sign-in; set external_url to pin it)"},
		"unknown":    {model.DaemonInfo{Port: 9477}, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			mux.HandleFunc("/api/daemon", func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(tc.info)
			})
			f := serveStatusSocket(t, mux)

			var buf bytes.Buffer
			require.NoError(t, runStatus(t.Context(), &buf, f, remoteFlags{}, false))
			if tc.want == "" {
				assert.NotContains(t, buf.String(), "Web UI:")
			} else {
				assert.Contains(t, buf.String(), tc.want)
			}
		})
	}
}

// A paused schedule records no cron runs, so status is where it shows up.
func TestRunStatus_ListsPausedSchedules(t *testing.T) {
	pausedAt := time.Date(2026, 9, 29, 14, 0, 0, 0, time.UTC)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(server.TasksResponseBody{Items: []model.TaskResponse{
			{Task: model.Task{Name: "nightly", Cron: "0 3 * * *", ManualTrigger: true}, PausedAt: &pausedAt},
			{Task: model.Task{Name: "hourly", Cron: "0 * * * *", ManualTrigger: true}},
		}})
	})
	f := serveStatusSocket(t, mux)

	var buf bytes.Buffer
	require.NoError(t, runStatus(t.Context(), &buf, f, remoteFlags{}, false))
	out := buf.String()
	assert.Contains(t, out, "1 cron schedule is paused:\n    nightly\n")
	assert.Contains(t, out, "runwisp resume <task>")
	assert.NotContains(t, out, "hourly")
}

func TestRunStatus_HealthNon200Errors(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	f := serveStatusSocket(t, mux)

	var buf bytes.Buffer
	err := runStatus(t.Context(), &buf, f, remoteFlags{}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not reachable")
}

func TestRunStatus_UnreachableErrors(t *testing.T) {
	t.Parallel()
	// No socket created — HealthCheck fails to dial.
	f := Flags{DataDir: testutil.ShortTempDir(t)}
	var buf bytes.Buffer
	err := runStatus(t.Context(), &buf, f, remoteFlags{}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not reachable")
}

// serveTasks answers /api/tasks with tasks, the way a daemon reports its live
// task list, plus /health so connectLocal and the remote probe pass.
func serveTasks(mux *http.ServeMux, tasks ...model.TaskResponse) *http.ServeMux {
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/tasks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(server.TasksResponseBody{Items: tasks})
	})
	return mux
}

func fatalWorker() model.TaskResponse {
	return model.TaskResponse{
		Task: model.Task{Name: "worker", Kind: model.KindService, Instances: 1, ManualTrigger: true},
		Service: &model.ServiceStatus{State: "fatal", DesiredInstances: 1, Instances: []model.ServiceInstanceStatus{
			{Index: 0, State: "fatal", StartFails: 11},
		}},
	}
}

// A service that gave up restarting stays down until someone starts it, and
// status used to print nothing about it.
func TestRunStatus_ListsDownServices(t *testing.T) {
	healthy := model.TaskResponse{
		Task:    model.Task{Name: "api", Kind: model.KindService, Instances: 2, ManualTrigger: true},
		Service: &model.ServiceStatus{State: "running", DesiredInstances: 2, RunningInstances: 2},
	}
	f := serveStatusSocket(t, serveTasks(http.NewServeMux(), fatalWorker(), healthy))

	var buf bytes.Buffer
	require.NoError(t, runStatus(t.Context(), &buf, f, remoteFlags{}, false))
	out := buf.String()
	assert.Contains(t, out, "✗ 1 service is down: worker (FATAL, 0/1 running)\n")
	assert.Contains(t, out, "runwisp start worker")
	assert.NotContains(t, out, "api (")

	buf.Reset()
	require.NoError(t, runStatus(t.Context(), &buf, f, remoteFlags{}, true))
	var doc statusJSONDoc
	require.NoError(t, json.Unmarshal(buf.Bytes(), &doc))
	require.Len(t, doc.Tasks, 2)
	require.NotNil(t, doc.Tasks[0].Service, "status --json carries the service state")
	assert.Equal(t, "fatal", doc.Tasks[0].Service.State)
}

// remoteAuth adds the CHAP endpoints a remote login walks through, counting
// logins, and makes every other /api/ call require the fresh token.
func remoteAuth(mux *http.ServeMux, logins *atomic.Int32) http.Handler {
	mux.HandleFunc("/api/auth/challenge", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"nonce": "test-nonce"})
	})
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		logins.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]string{"token": freshToken})
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/api/auth/") &&
			r.Header.Get("Authorization") != "Bearer "+freshToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// status had no --url, so checking a remote daemon meant ssh. An expired
// cached session must log in again instead of printing a bare "healthy".
func TestRunStatusURLReachesTheRemoteDaemon(t *testing.T) {
	useTempCacheDir(t)
	mux := serveTasks(http.NewServeMux(), fatalWorker())
	mux.HandleFunc("/api/daemon", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(model.DaemonInfo{Port: 9999})
	})
	var logins atomic.Int32
	srv := httptest.NewServer(remoteAuth(mux, &logins))
	defer srv.Close()
	storeCachedToken(srv.URL, "", testSessions.IssueToken(time.Hour)) // one the daemon no longer accepts

	// No socket in this data dir: only the remote daemon can answer.
	f := Flags{DataDir: testutil.ShortTempDir(t)}
	var buf bytes.Buffer
	require.NoError(t, runStatus(t.Context(), &buf, f, remoteFlags{URL: srv.URL, Password: "pw"}, false))
	out := buf.String()
	assert.Contains(t, out, "RunWisp is healthy at :9999")
	assert.Contains(t, out, "worker (FATAL, 0/1 running)")
	assert.Equal(t, int32(1), logins.Load(), "the stale session is replaced by one login")
}
