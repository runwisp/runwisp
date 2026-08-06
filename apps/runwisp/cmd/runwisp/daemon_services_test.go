// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
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

// daemonServicesTestEnv prepares a writable data dir + in-memory DB and returns
// Flags pointing at the dir so the helpers under test can resolve f.LogDir().
func daemonServicesTestEnv(t *testing.T) (Flags, storage.Database) {
	t.Helper()
	f := Flags{DataDir: t.TempDir()}

	db, err := storage.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return f, db
}

func TestInitExecutor_BuildsExecutorWithEventBus(t *testing.T) {
	f, _ := daemonServicesTestEnv(t)
	cfg := &config.Config{
		Tasks: []model.Task{
			{Name: "alpha", Run: "echo a", Kind: model.KindTask, OnOverlap: model.PolicyQueue, MaxConcurrent: 1},
		},
	}
	config.ApplyDefaults(cfg)
	bus := events.NewEventBus()
	exec := initExecutor(cfg, bus, f.LogDir(), "")
	require.NotNil(t, exec)
	avail := exec.Availability()
	// HTTP is always available; Config flips on with at least one local task.
	assert.True(t, avail.HTTP.Available)
	assert.True(t, avail.Config.Available)
}

func TestInitTaskManager_PopulatesTasksMap(t *testing.T) {
	f, db := daemonServicesTestEnv(t)
	cfg := &config.Config{
		Tasks: []model.Task{
			{Name: "alpha", Run: "echo a", Kind: model.KindTask, OnOverlap: model.PolicyQueue, MaxConcurrent: 1},
			{Name: "beta", Run: "echo b", Kind: model.KindTask, OnOverlap: model.PolicyQueue, MaxConcurrent: 1},
		},
	}
	config.ApplyDefaults(cfg)

	bus := events.NewEventBus()
	exec := initExecutor(cfg, bus, f.LogDir(), "")
	dc := &daemonConfig{Config: cfg}

	tm, tasksMap := initTaskManager(dc, db, exec, bus)
	require.NotNil(t, tm)
	require.Len(t, tasksMap, 2)
	assert.Contains(t, tasksMap, "alpha")
	assert.Contains(t, tasksMap, "beta")
}

func TestInitRetentionCleaner_StartsAndStops(t *testing.T) {
	f, db := daemonServicesTestEnv(t)
	cfg := &config.Config{}
	config.ApplyDefaults(cfg)
	dc := &daemonConfig{Config: cfg}

	cleaner := initRetentionCleaner(dc, db, runtime.NewTaskRegistry(nil), f.LogDir())
	require.NotNil(t, cleaner)
	t.Cleanup(cleaner.Stop)
}

func TestResumePendingRuns_EmptyDBReturnsEmptySummary(t *testing.T) {
	f, db := daemonServicesTestEnv(t)
	cfg := &config.Config{}
	config.ApplyDefaults(cfg)
	bus := events.NewEventBus()
	exec := initExecutor(cfg, bus, f.LogDir(), "")
	dc := &daemonConfig{Config: cfg}
	tm, _ := initTaskManager(dc, db, exec, bus)

	summary := resumePendingRuns(t.Context(), db, tm)
	assert.Equal(t, uikit.PendingRunsSummary{}, summary)
}

func TestStartServiceInstances_SkipsNonServiceTasks(t *testing.T) {
	f, db := daemonServicesTestEnv(t)
	cfg := &config.Config{
		Tasks: []model.Task{
			{Name: "cron-task", Run: "echo cron", Kind: model.KindTask, OnOverlap: model.PolicyQueue, MaxConcurrent: 1},
		},
	}
	config.ApplyDefaults(cfg)
	bus := events.NewEventBus()
	exec := initExecutor(cfg, bus, f.LogDir(), "")
	dc := &daemonConfig{Config: cfg}
	tm, tasksMap := initTaskManager(dc, db, exec, bus)

	// Should not panic, even though there are no service tasks.
	startServiceInstances(t.Context(), tm, tasksMap)
}

func TestBuildDaemonInfo_PopulatesTaskList(t *testing.T) {
	f, db := daemonServicesTestEnv(t)
	cfg := &config.Config{
		Tasks: []model.Task{
			{Name: "zulu", Run: "echo z", Kind: model.KindTask, OnOverlap: model.PolicyQueue, MaxConcurrent: 1},
			{Name: "alpha", Run: "echo a", Kind: model.KindTask, OnOverlap: model.PolicyQueue, MaxConcurrent: 1},
		},
		Scheduler: config.Scheduler{Timezone: "UTC", Source: "system"},
	}
	config.ApplyDefaults(cfg)

	bus := events.NewEventBus()
	exec := initExecutor(cfg, bus, f.LogDir(), "")
	dc := &daemonConfig{
		Config:      cfg,
		Fingerprint: "fp-test",
	}
	tm, tasksMap := initTaskManager(dc, db, exec, bus)

	svc := &daemonServices{
		Executor:            exec,
		TaskManager:         tm,
		Tasks:               runtime.NewTaskRegistry(tasksMap),
		TaskShutdownTimeout: 5 * time.Second,
	}
	info := buildDaemonInfo(dc, svc, time.Time{}, f.Port)
	require.NotNil(t, info)
	assert.Equal(t, "fp-test", info.Fingerprint)
	require.Len(t, info.Tasks, 2)
	// Tasks must be sorted by name.
	assert.Equal(t, "alpha", info.Tasks[0].Name)
	assert.Equal(t, "zulu", info.Tasks[1].Name)
	// Capabilities are populated from executor availability.
	assert.Len(t, info.Capabilities, 5)
}

func TestOrderServicesForStart(t *testing.T) {
	tasksMap := map[string]*model.Task{
		"cron":  {Name: "cron", Kind: model.KindTask},
		"beta":  {Name: "beta", Kind: model.KindService, Priority: 10},
		"alpha": {Name: "alpha", Kind: model.KindService, Priority: 10},
		"first": {Name: "first", Kind: model.KindService, Priority: -5},
		"last":  {Name: "last", Kind: model.KindService, Priority: 100},
	}

	got := orderServicesForStart(tasksMap)

	names := make([]string, len(got))
	for i, task := range got {
		names[i] = task.Name
	}
	// Ascending priority; equal priorities fall back to alphabetical name.
	assert.Equal(t, []string{"first", "alpha", "beta", "last"}, names)
}

func TestOrderServicesForStart_DropsNonServices(t *testing.T) {
	tasksMap := map[string]*model.Task{
		"job": {Name: "job", Kind: model.KindTask},
	}
	assert.Empty(t, orderServicesForStart(tasksMap))
}

// TestOrderServicesForStop_DependentsFirst checks the reverse-dependency
// teardown order over a diamond (a→b, a→c, b→d, c→d): every service must appear
// before each service it depends on.
func TestOrderServicesForStop_DependentsFirst(t *testing.T) {
	tasksMap := map[string]*model.Task{
		"a": {Name: "a", Kind: model.KindService, DependsOn: []string{"b", "c"}},
		"b": {Name: "b", Kind: model.KindService, DependsOn: []string{"d"}},
		"c": {Name: "c", Kind: model.KindService, DependsOn: []string{"d"}},
		"d": {Name: "d", Kind: model.KindService},
		"x": {Name: "x", Kind: model.KindTask}, // non-service is dropped
	}

	got := orderServicesForStop(tasksMap)
	pos := make(map[string]int, len(got))
	for i, task := range got {
		assert.Equal(t, model.KindService, task.Kind, "non-services must be dropped")
		pos[task.Name] = i
	}
	require.Len(t, got, 4)

	// A service is stopped before any service it depends on.
	assert.Less(t, pos["a"], pos["b"], "a depends on b → a stops first")
	assert.Less(t, pos["a"], pos["c"], "a depends on c → a stops first")
	assert.Less(t, pos["b"], pos["d"], "b depends on d → b stops first")
	assert.Less(t, pos["c"], pos["d"], "c depends on d → c stops first")
}

// TestBuildDaemonInfo_SchedulingActiveReflectsScheduler locks the wiring that
// drives the Web UI's cloud-mode reframe: scheduling_active must be false when
// the local scheduler is absent (e.g. `runwisp cloud`, where the cloud owns
// scheduling) and true when it is present. Drift here makes a scheduled task
// look unscheduled — a Prime-Directive-#1 ("nothing silently fails") violation.
func TestBuildDaemonInfo_SchedulingActiveReflectsScheduler(t *testing.T) {
	f, db := daemonServicesTestEnv(t)
	cfg := &config.Config{
		Tasks:     []model.Task{{Name: "alpha", Run: "echo a", Kind: model.KindTask, OnOverlap: model.PolicyQueue, MaxConcurrent: 1}},
		Scheduler: config.Scheduler{Timezone: "UTC", Source: "system"},
	}
	config.ApplyDefaults(cfg)

	bus := events.NewEventBus()
	exec := initExecutor(cfg, bus, f.LogDir(), "")
	dc := &daemonConfig{Config: cfg, Fingerprint: "fp-test"}
	tm, tasksMap := initTaskManager(dc, db, exec, bus)

	svc := &daemonServices{Executor: exec, TaskManager: tm, Tasks: runtime.NewTaskRegistry(tasksMap)}

	// Cloud mode: no local scheduler.
	assert.False(t, buildDaemonInfo(dc, svc, time.Time{}, f.Port).SchedulingActive)

	// Standalone mode: scheduler present.
	svc.Scheduler = runtime.NewScheduler(tm, tasksMap, time.UTC, nil)
	assert.True(t, buildDaemonInfo(dc, svc, time.Time{}, f.Port).SchedulingActive)
}
