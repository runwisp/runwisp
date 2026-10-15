// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/station"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReload_StationMode proves `runwisp reload` works in station mode: the
// changed task is applied through the normal reconciler with no local
// scheduler, and the station client re-syncs the task list to the Station
// without a reconnect (the test Station never accepts the WebSocket, so the
// only way it can see the new task is the reload-triggered sync).
func TestReload_StationMode(t *testing.T) {
	f, db := daemonServicesTestEnv(t)
	f.CfgFile = filepath.Join(t.TempDir(), "runwisp.toml")
	write := func(run string) {
		body := "[tasks.alpha]\nrun = \"" + run + "\"\ncron = \"0 3 * * *\"\n"
		require.NoError(t, os.WriteFile(f.CfgFile, []byte(body), 0o600))
	}
	write("echo before")

	var mu sync.Mutex
	var bodies []string
	stationSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/runner/tasks/sync" {
			http.NotFound(w, r) // the WS dial: the client just backs off
			return
		}
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		_, _ = w.Write([]byte(`{"success":true,"summary":{}}`))
	}))
	defer stationSrv.Close()
	baseURL, err := url.Parse(stationSrv.URL)
	require.NoError(t, err)

	cfg, err := loadConfig(config.FileSource(f.CfgFile), true)
	require.NoError(t, err)
	dc := &daemonConfig{
		Config:        cfg,
		StationConfig: station.Config{Enabled: true, BaseURL: baseURL, StationToken: "tok", AgentVersion: "0.0.0"},
	}
	svc, err := initDaemonServices(t.Context(), dc, db, modeStation, f)
	require.NoError(t, err)
	t.Cleanup(func() {
		svc.RetentionCleaner.Stop()
		svc.SoftDeletePurger.Stop()
		svc.MemoryReclaimer.Stop()
		svc.ServiceLaunchCancel()
	})
	require.Nil(t, svc.Scheduler, "station mode must not start a local scheduler")

	client, err := station.NewClient(dc.StationConfig, station.Dependencies{
		TaskManager: svc.TaskManager,
		RunRepo:     svc.DB,
		EventBus:    svc.EventBus,
		LocalTasks:  svc.Tasks,
		LogDir:      f.LogDir(),
		Now:         time.Now,
	})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); _ = client.Run(ctx) }()
	defer func() { cancel(); <-done }()

	settings := &settingsApplier{svc: svc}
	_, reloadFn := newReconciler(dc, svc, config.FileSource(f.CfgFile), config.NewSnapshot(f.CfgFile, cfg, time.Now()), settings)

	write("echo after")
	// Run subscribes asynchronously; retry the (idempotent) reload until the
	// Station has seen it rather than sleeping.
	require.Eventually(t, func() bool {
		if _, err := reloadFn(); err != nil {
			return false
		}
		mu.Lock()
		defer mu.Unlock()
		for _, b := range bodies {
			if strings.Contains(b, "echo after") {
				return true
			}
		}
		return false
	}, 5*time.Second, 50*time.Millisecond, "station never received the reloaded task list")

	got, ok := svc.Tasks.Get("alpha")
	require.True(t, ok)
	assert.Equal(t, "echo after", got.Run)
	assert.Nil(t, svc.Scheduler)
}
