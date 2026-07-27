// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// setupServerForMetrics builds a minimal Server wired for /metrics gating tests.
// It mirrors setupServer but lets the caller flip the metrics knobs and also
// passes a SocketPath so callers can invoke Start() for the separate-listener
// lifecycle check.
func setupServerForMetrics(t *testing.T, metricsEnabled bool, metricsListen string) (*Server, *testutil.MockRunRepository) {
	t.Helper()
	repo := new(testutil.MockRunRepository)
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := runtime.NewTaskManager(exec, eb, time.Now)
	tasks := map[string]*model.Task{}
	scheduler := runtime.NewScheduler(jm, tasks, time.UTC)
	tmpDir := testutil.ShortTempDir(t)

	s, err := New(Options{
		DB:             repo,
		TaskManager:    jm,
		Tasks:          runtime.NewTaskRegistry(tasks),
		Scheduler:      scheduler,
		Host:           "127.0.0.1",
		Port:           testutil.PickFreePort(t),
		SocketPath:     filepath.Join(tmpDir, "runwisp.sock"),
		LogDir:         tmpDir,
		EventBus:       eb,
		Password:       "secret",
		JWTSecret:      "test-jwt-secret",
		MetricsEnabled: metricsEnabled,
		MetricsListen:  metricsListen,
		DaemonInfo:     &model.DaemonInfo{},
	})
	require.NoError(t, err)
	return s, repo
}

// TestMetricsRouter_DisabledByDefaultExposesNoMetrics pins the safety-first
// default: a fresh daemon with no [daemon] metrics_enabled key in the TOML
// returns no OpenMetrics payload. (The SPA catch-all serves index.html for
// unmounted paths, so the assertion is on payload shape, not status code.)
func TestMetricsRouter_DisabledByDefaultExposesNoMetrics(t *testing.T) {
	s, _ := setupServerForMetrics(t, false, "")

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	body := w.Body.String()
	assert.NotContains(t, body, "runwisp_",
		"metrics_enabled=false must not leak any runwisp_* metric on the main router")
	assert.NotContains(t, w.Header().Get("Content-Type"), "openmetrics",
		"metrics_enabled=false must not respond with the OpenMetrics content type")
}

// TestMetricsRouter_EnabledMountsOnMainRouter covers the simplest opt-in
// shape: metrics_enabled=true with no separate listener exposes /metrics on
// the same chi mux as /health.
func TestMetricsRouter_EnabledMountsOnMainRouter(t *testing.T) {
	s, repo := setupServerForMetrics(t, true, "")
	repo.On("GetRunSummary", mock.Anything).Return(&model.RunSummary{Total: 0}, nil)

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "runwisp_runs_total")
}

// TestMetricsRouter_SeparateListenerUnmountsFromMain guards the regression
// where the separate-listener path forgot to remove /metrics from the main
// mux — leaking the same data twice and defeating the point of the split.
// The SPA catch-all still serves index.html for unmounted paths, so we
// assert on payload shape rather than status code.
func TestMetricsRouter_SeparateListenerUnmountsFromMain(t *testing.T) {
	s, _ := setupServerForMetrics(t, true, "127.0.0.1:0")

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	body := w.Body.String()
	assert.NotContains(t, body, "runwisp_runs_total",
		"when metrics_listen is set, the OpenMetrics payload must not also appear on the main mux")
	assert.NotContains(t, w.Header().Get("Content-Type"), "openmetrics",
		"main mux must not respond with the OpenMetrics content type when a dedicated listener is configured")
}

// TestMetricsServer_DedicatedListenerServesAndShutsDown exercises the live
// bind/unbind flow for the separate-listener path: Start() spins up the
// metrics http.Server on the configured address, a scrape over real TCP
// returns the payload, and Shutdown() drains the listener cleanly.
func TestMetricsServer_DedicatedListenerServesAndShutsDown(t *testing.T) {
	metricsPort := testutil.PickFreePort(t)
	metricsAddr := fmt.Sprintf("127.0.0.1:%d", metricsPort)
	s, repo := setupServerForMetrics(t, true, metricsAddr)
	repo.On("GetRunSummary", mock.Anything).Return(&model.RunSummary{Total: 0}, nil)

	errCh := make(chan error, 1)
	go func() { errCh <- s.Start() }()

	// Wait for the metrics listener to accept connections.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", metricsAddr, 100*time.Millisecond)
		if err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + metricsAddr + "/metrics")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, string(body), "runwisp_runs_total")
	assert.True(t, strings.HasSuffix(string(body), "# EOF\n"),
		"separate-listener path must return the same OpenMetrics shape, # EOF terminator and all")

	// Anything other than /metrics on the dedicated listener is 404 — the
	// dedicated mux must not expose UI, REST, or auth surfaces.
	resp2, err := client.Get("http://" + metricsAddr + "/health")
	require.NoError(t, err)
	resp2.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp2.StatusCode)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, s.Shutdown(ctx))
	<-errCh
}
