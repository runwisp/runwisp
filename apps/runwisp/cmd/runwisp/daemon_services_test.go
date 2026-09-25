// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/runwisp/runwisp/internal/tui/uikit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
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
	// HTTP requires the allow_station_dispatch opt-in (not set here); Config flips
	// on with at least one local task regardless of the opt-in.
	assert.False(t, avail.HTTP.Available)
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

	cleaner := initRetentionCleaner(dc, db, runtime.NewTaskRegistry(nil), f.LogDir(), nil)
	require.NotNil(t, cleaner)
	t.Cleanup(cleaner.Stop)
}

// TestMarkCrashedRunsWithRetry_RetriesTransientFailure locks in the boot-time
// retry: a transient DB error (e.g. SQLite busy) must not permanently skip
// crash recovery for the whole boot — it succeeds once the underlying error
// clears within the bounded retry budget, and no warning is raised.
func TestMarkCrashedRunsWithRetry_RetriesTransientFailure(t *testing.T) {
	db := new(testutil.MockRunRepository)
	db.On("MarkCrashedRuns", mock.Anything).Return(int64(0), errors.New("database is locked")).Twice()
	db.On("MarkCrashedRuns", mock.Anything).Return(int64(3), nil).Once()

	crashed, err := markCrashedRunsWithRetry(t.Context(), db)

	require.NoError(t, err, "a transient failure that clears within the retry budget must not surface an error")
	assert.Equal(t, int64(3), crashed)
	db.AssertExpectations(t)
}

// TestMarkCrashedRunsWithRetry_ErrorsAfterExhaustingRetries locks in that crash
// recovery is a boot precondition: once every attempt in the bounded budget has
// failed it returns an error so the caller aborts boot rather than proceeding
// with runs stuck at 'running' forever.
func TestMarkCrashedRunsWithRetry_ErrorsAfterExhaustingRetries(t *testing.T) {
	db := new(testutil.MockRunRepository)
	db.On("MarkCrashedRuns", mock.Anything).Return(int64(0), errors.New("database is locked"))

	crashed, err := markCrashedRunsWithRetry(t.Context(), db)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "mark crashed runs")
	assert.Equal(t, int64(0), crashed)
	db.AssertNumberOfCalls(t, "MarkCrashedRuns", 3)
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
	assert.Equal(t, "alpha", info.Tasks[0].Name)
	assert.Equal(t, "zulu", info.Tasks[1].Name)
	assert.Len(t, info.Capabilities, 5)
	assert.False(t, info.CheckUpdates)

	cfg.Daemon.CheckUpdates = true
	assert.True(t, buildDaemonInfo(dc, svc, time.Time{}, f.Port).CheckUpdates,
		"check_updates must reach the Web UI, which gates its feedback prompt on it")
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
		"x": {Name: "x", Kind: model.KindTask},
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

// TestInitDaemonServices_StationModeResolvesPendingRuns locks the crash-safety
// invariant ("any run that was in-flight is marked interrupted with a terminal
// status — it is not resumed") across every boot mode, not just standalone.
// resumePendingRuns is only called from startStandaloneScheduling, which
// initDaemonServices gates on mode == modeStandalone, so a run a prior crash
// left at status='pending' must still be resolved when the daemon boots into
// station mode instead.
func TestInitDaemonServices_StationModeResolvesPendingRuns(t *testing.T) {
	f, db := daemonServicesTestEnv(t)
	cfg := &config.Config{}
	config.ApplyDefaults(cfg)
	dc := &daemonConfig{Config: cfg}

	pending := &model.Run{ID: ulid.Make().String(), TaskName: "ghost", Status: model.PhasePending, TriggeredBy: model.TriggeredByCron}
	require.NoError(t, db.CreateRun(t.Context(), pending))

	svc, err := initDaemonServices(t.Context(), dc, db, modeStation, f)
	require.NoError(t, err)
	t.Cleanup(func() {
		svc.RetentionCleaner.Stop()
		svc.SoftDeletePurger.Stop()
		svc.MemoryReclaimer.Stop()
		svc.ServiceLaunchCancel()
	})

	got, err := db.GetRun(t.Context(), pending.ID)
	require.NoError(t, err)
	assert.NotEqual(t, model.PhasePending, got.Status,
		"a run left pending by a prior crash must be resolved to a terminal status on boot, even in station mode")
}

// TestBuildDaemonInfo_SchedulingActiveReflectsScheduler locks the wiring that
// drives the Web UI's station-mode reframe: scheduling_active must be false when
// the local scheduler is absent (e.g. `runwisp station`, where the station owns
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

	// Station mode: no local scheduler.
	assert.False(t, buildDaemonInfo(dc, svc, time.Time{}, f.Port).SchedulingActive)

	// Standalone mode: scheduler present.
	svc.Scheduler = runtime.NewScheduler(tm, tasksMap, time.UTC, nil)
	assert.True(t, buildDaemonInfo(dc, svc, time.Time{}, f.Port).SchedulingActive)
}
