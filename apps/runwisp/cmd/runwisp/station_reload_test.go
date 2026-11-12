// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/station"
	"github.com/runwisp/runwisp/apps/runwisp/internal/station/stationtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReload_StationMode proves `runwisp reload` works in station mode: the
// changed task is applied through the normal reconciler with no local
// scheduler, and the station client sends the new catalog over the open
// session (tasks:sync) without a reconnect.
func TestReload_StationMode(t *testing.T) {
	f, db := daemonServicesTestEnv(t)
	f.CfgFile = filepath.Join(t.TempDir(), "runwisp.toml")
	write := func(run string) {
		body := "[tasks.alpha]\nrun = \"" + run + "\"\ncron = \"0 3 * * *\"\n"
		require.NoError(t, os.WriteFile(f.CfgFile, []byte(body), 0o600))
	}
	write("echo before")

	st := stationtest.New(t)
	cfg, err := loadConfig(config.FileSource(f.CfgFile), true)
	require.NoError(t, err)
	dc := &daemonConfig{
		Config:        cfg,
		StationConfig: station.Config{Enabled: true, BaseURL: st.URL, StationToken: "tok", AgentVersion: "0.0.0"},
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
		Outbox:      svc.DB,
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
	st.Wait(t, "hello", nil)

	settings := &settingsApplier{svc: svc}
	_, reloadFn := newReconciler(dc, svc, config.FileSource(f.CfgFile), config.NewSnapshot(f.CfgFile, cfg, time.Now()), settings)

	write("echo after")
	// The session attaches just after welcome; retry the (idempotent) reload
	// until the station has seen it rather than sleeping.
	require.Eventually(t, func() bool {
		if _, err := reloadFn(); err != nil {
			return false
		}
		return st.Count("tasks:sync") > 0
	}, 5*time.Second, 50*time.Millisecond, "station never received the reloaded task list")
	sync := stationtest.Decode[protocol.TasksSync](t, st, "tasks:sync", nil)
	require.Len(t, sync.Tasks, 1)
	assert.Contains(t, string(sync.Tasks[0].Spec), "echo after")

	got, ok := svc.Tasks.Get("alpha")
	require.True(t, ok)
	assert.Equal(t, "echo after", got.Run)
	assert.Nil(t, svc.Scheduler)
}
