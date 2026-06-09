// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCapInfosFromAvailability_ExposesEveryBackend(t *testing.T) {
	a := executor.Availability{
		Shell:     executor.BackendStatus{Available: true},
		Container: executor.BackendStatus{Available: false, Reason: "no docker"},
		Compose:   executor.BackendStatus{Available: true},
		HTTP:      executor.BackendStatus{Available: true},
		Config:    executor.BackendStatus{Available: false},
	}
	caps := capInfosFromAvailability(a)
	assert.Len(t, caps, 5)

	got := map[string]bool{}
	for _, c := range caps {
		got[c.Name] = c.Available
	}
	assert.Equal(t, map[string]bool{
		"shell":     true,
		"container": false,
		"compose":   true,
		"http":      true,
		"config":    false,
	}, got)
}

func TestCapInfosFromAvailability_PreservesOrder(t *testing.T) {
	caps := capInfosFromAvailability(executor.Availability{})
	require := assert.New(t)
	require.Equal("shell", caps[0].Name)
	require.Equal("container", caps[1].Name)
	require.Equal("compose", caps[2].Name)
	require.Equal("http", caps[3].Name)
	require.Equal("config", caps[4].Name)
}

// daemonServicesTestEnv prepares a writable data dir + in-memory DB so the
// helpers under test can call flags.LogDir() safely.
func daemonServicesTestEnv(t *testing.T) storage.Database {
	t.Helper()
	dir := t.TempDir()
	origDataDir := flags.DataDir
	flags.DataDir = dir
	t.Cleanup(func() { flags.DataDir = origDataDir })

	db, err := storage.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestInitExecutor_BuildsExecutorWithEventBus(t *testing.T) {
	daemonServicesTestEnv(t)
	cfg := &config.Config{
		Tasks: []model.Task{
			{Name: "alpha", Run: "echo a", Kind: model.KindTask, OnOverlap: model.PolicyQueue, MaxConcurrent: 1},
		},
	}
	config.ApplyDefaults(cfg)
	bus := events.NewEventBus()
	exec := initExecutor(cfg, bus)
	require.NotNil(t, exec)
	avail := exec.Availability()
	// HTTP is always available; Config flips on with at least one local task.
	assert.True(t, avail.HTTP.Available)
	assert.True(t, avail.Config.Available)
}

func TestInitTaskManager_PopulatesTasksMap(t *testing.T) {
	db := daemonServicesTestEnv(t)
	cfg := &config.Config{
		Tasks: []model.Task{
			{Name: "alpha", Run: "echo a", Kind: model.KindTask, OnOverlap: model.PolicyQueue, MaxConcurrent: 1},
			{Name: "beta", Run: "echo b", Kind: model.KindTask, OnOverlap: model.PolicyQueue, MaxConcurrent: 1},
		},
	}
	config.ApplyDefaults(cfg)

	bus := events.NewEventBus()
	exec := initExecutor(cfg, bus)
	dc := &daemonConfig{Config: cfg}

	tm, tasksMap := initTaskManager(dc, db, exec, bus)
	require.NotNil(t, tm)
	require.Len(t, tasksMap, 2)
	assert.Contains(t, tasksMap, "alpha")
	assert.Contains(t, tasksMap, "beta")
}

func TestInitRetentionCleaner_StartsAndStops(t *testing.T) {
	db := daemonServicesTestEnv(t)
	cfg := &config.Config{}
	config.ApplyDefaults(cfg)
	dc := &daemonConfig{Config: cfg}

	cleaner := initRetentionCleaner(dc, db, map[string]*model.Task{})
	require.NotNil(t, cleaner)
	t.Cleanup(cleaner.Stop)
}

func TestResumePendingRuns_EmptyDBReturnsEmptySummary(t *testing.T) {
	db := daemonServicesTestEnv(t)
	cfg := &config.Config{}
	config.ApplyDefaults(cfg)
	bus := events.NewEventBus()
	exec := initExecutor(cfg, bus)
	dc := &daemonConfig{Config: cfg}
	tm, _ := initTaskManager(dc, db, exec, bus)

	summary := resumePendingRuns(t.Context(), db, tm)
	assert.Equal(t, uikit.PendingRunsSummary{}, summary)
}

func TestStartServiceInstances_SkipsNonServiceTasks(t *testing.T) {
	db := daemonServicesTestEnv(t)
	cfg := &config.Config{
		Tasks: []model.Task{
			{Name: "cron-task", Run: "echo cron", Kind: model.KindTask, OnOverlap: model.PolicyQueue, MaxConcurrent: 1},
		},
	}
	config.ApplyDefaults(cfg)
	bus := events.NewEventBus()
	exec := initExecutor(cfg, bus)
	dc := &daemonConfig{Config: cfg}
	tm, tasksMap := initTaskManager(dc, db, exec, bus)

	// Should not panic, even though there are no service tasks.
	startServiceInstances(tm, tasksMap)
}

func TestBuildDaemonInfo_PopulatesTaskList(t *testing.T) {
	db := daemonServicesTestEnv(t)
	cfg := &config.Config{
		Tasks: []model.Task{
			{Name: "zulu", Run: "echo z", Kind: model.KindTask, OnOverlap: model.PolicyQueue, MaxConcurrent: 1},
			{Name: "alpha", Run: "echo a", Kind: model.KindTask, OnOverlap: model.PolicyQueue, MaxConcurrent: 1},
		},
		Scheduler: config.Scheduler{Timezone: "UTC", Source: "system"},
	}
	config.ApplyDefaults(cfg)

	bus := events.NewEventBus()
	exec := initExecutor(cfg, bus)
	dc := &daemonConfig{
		Config:      cfg,
		Fingerprint: "fp-test",
	}
	tm, tasksMap := initTaskManager(dc, db, exec, bus)

	svc := &daemonServices{
		Executor:            exec,
		TaskManager:         tm,
		TasksMap:            tasksMap,
		TaskShutdownTimeout: 5 * time.Second,
	}
	info := buildDaemonInfo(dc, svc, time.Time{})
	require.NotNil(t, info)
	assert.Equal(t, "fp-test", info.Fingerprint)
	require.Len(t, info.Tasks, 2)
	// Tasks must be sorted by name.
	assert.Equal(t, "alpha", info.Tasks[0].Name)
	assert.Equal(t, "zulu", info.Tasks[1].Name)
	// Capabilities are populated from executor availability.
	assert.Len(t, info.Capabilities, 5)
}
