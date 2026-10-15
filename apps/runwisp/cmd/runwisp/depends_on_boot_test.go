// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/executor"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/apps/runwisp/internal/testutil"
	"github.com/runwisp/runwisp/apps/runwisp/internal/testutil/fakeclock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bootTestService builds a minimal always-on service for the boot/teardown
// tests. healthyAfter governs how long an instance must stay live to count as
// healthy — the readiness bar a dependent waits on.
func bootTestService(name string, healthyAfter time.Duration, dependsOn ...string) *model.Task {
	restartAttempts := 3
	return &model.Task{
		Name:            name,
		Kind:            model.KindService,
		Run:             "sleep 1",
		Restart:         model.RestartAlways,
		MaxConcurrent:   1,
		OnOverlap:       model.PolicySkip,
		Instances:       1,
		Autostart:       true,
		HealthyAfter:    &healthyAfter,
		RestartAttempts: &restartAttempts,
		RestartBackoff:  model.BackoffConstant,
		DependsOn:       dependsOn,
	}
}

// stopOrderExecutor records the task of each run whose Execute returned.
type stopOrderExecutor struct {
	*testutil.GateExecutor
	mu    sync.Mutex
	order []string
}

func (e *stopOrderExecutor) Execute(ctx context.Context, task *model.Task, run *model.Run) *executor.ExecuteResult {
	res := e.GateExecutor.Execute(ctx, task, run)
	e.mu.Lock()
	e.order = append(e.order, task.Name)
	e.mu.Unlock()
	return res
}

func (e *stopOrderExecutor) stopped() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.order)
}

func activeCount(tm runtime.TaskManager, name string) int {
	return len(tm.GetActiveRuns(name))
}

// TestStartServiceInstances_HonorsAutostart proves the boot launcher starts an
// autostart service but leaves a non-autostart one stopped. This is the
// invariant that lets initDaemonServices run the launcher unconditionally (in
// station mode too): a non-autostart service boots stopped because its supervisor
// is created stopped and StartServiceInstances no-ops on a stopped supervisor.
func TestStartServiceInstances_HonorsAutostart(t *testing.T) {
	exec := testutil.NewGateExecutor()
	bus := events.NewEventBus()
	tm := runtime.NewTaskManager(exec, bus, time.Now)
	t.Cleanup(tm.Shutdown)

	on := bootTestService("on", time.Nanosecond)
	off := bootTestService("off", time.Nanosecond)
	off.Autostart = false
	tm.UpsertTask(on)
	tm.UpsertTask(off)
	tasksMap := map[string]*model.Task{"on": on, "off": off}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startServiceInstances(ctx, tm, tasksMap)

	require.Eventually(t, func() bool { return activeCount(tm, "on") == 1 },
		2*time.Second, 10*time.Millisecond, "autostart service must come up at boot")
	// Let the non-autostart launcher goroutine run; it must never spawn an instance.
	time.Sleep(150 * time.Millisecond)
	assert.Equal(t, 0, activeCount(tm, "off"), "a non-autostart service must boot stopped")
}

// TestStartServiceInstances_GatesDependentOnDependencyHealth proves the launcher
// holds a dependent until its dependency is healthy: web must not start while db
// is still short of its healthy_after, then comes up once db crosses it. Health
// is read from the injected clock, so db only turns healthy when the test
// advances it; wall-clock scheduling jitter cannot let web through early.
func TestStartServiceInstances_GatesDependentOnDependencyHealth(t *testing.T) {
	exec := testutil.NewGateExecutor()
	bus := events.NewEventBus()
	clock := fakeclock.New(time.Now())
	tm := runtime.NewTaskManager(exec, bus, clock.Now)
	t.Cleanup(tm.Shutdown)

	// A long healthy_after keeps the launcher's wall-clock fallback window
	// (graceWindow + healthy_after) far beyond the test's runtime.
	db := bootTestService("db", time.Minute)
	web := bootTestService("web", time.Nanosecond, "db")
	tm.UpsertTask(db)
	tm.UpsertTask(web)
	tasksMap := map[string]*model.Task{"db": db, "web": web}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startServiceInstances(ctx, tm, tasksMap)

	require.Eventually(t, func() bool { return activeCount(tm, "db") == 1 },
		2*time.Second, 10*time.Millisecond, "db should start right away")
	// An ungated launcher would start web immediately; db cannot be healthy yet.
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, 0, activeCount(tm, "web"), "web must wait until db is healthy")

	clock.Advance(time.Minute)
	require.Eventually(t, func() bool { return activeCount(tm, "web") == 1 },
		2*time.Second, 10*time.Millisecond, "web should start once db is healthy")
}

// TestPreStopServices_StopsDependentBeforeDependency proves reverse-dependency
// teardown: web (the dependent) is fully drained before db (its dependency) is
// stopped.
func TestPreStopServices_StopsDependentBeforeDependency(t *testing.T) {
	// Record the order in which the services' runs actually stop. Terminal
	// events can't show it: each is published after its own async storage
	// flush, so db's can land before web's.
	exec := &stopOrderExecutor{GateExecutor: testutil.NewGateExecutor()}
	tm := runtime.NewTaskManager(exec, events.NewEventBus(), time.Now)
	t.Cleanup(tm.Shutdown)

	db := bootTestService("db", time.Nanosecond)
	web := bootTestService("web", time.Nanosecond, "db")
	tm.UpsertTask(db)
	tm.UpsertTask(web)
	tasksMap := map[string]*model.Task{"db": db, "web": web}

	require.NoError(t, tm.StartServiceInstances("db", model.TriggeredByService))
	require.NoError(t, tm.StartServiceInstances("web", model.TriggeredByService))
	require.Eventually(t, func() bool {
		return activeCount(tm, "db") == 1 && activeCount(tm, "web") == 1
	}, 2*time.Second, 10*time.Millisecond, "both services should be live")

	svc := &daemonServices{TaskManager: tm, Tasks: runtime.NewTaskRegistry(tasksMap)}

	// orderServicesForStop must list the dependent first.
	stopOrder := orderServicesForStop(tasksMap)
	require.Len(t, stopOrder, 2)
	assert.Equal(t, "web", stopOrder[0].Name)
	assert.Equal(t, "db", stopOrder[1].Name)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	preStopServices(ctx, svc)

	assert.Equal(t, 0, activeCount(tm, "web"), "web should be drained")
	assert.Equal(t, 0, activeCount(tm, "db"), "db should be drained")

	require.Equal(t, []string{"web", "db"}, exec.stopped(),
		"the dependent must stop before the dependency")
}
