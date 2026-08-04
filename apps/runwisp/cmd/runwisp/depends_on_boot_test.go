// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bootTestService builds a minimal always-on service for the boot/teardown
// tests. healthyAfter governs how long an instance must stay live to count as
// healthy — the readiness bar a dependent waits on.
func bootTestService(name string, healthyAfter time.Duration, dependsOn ...string) *model.Task {
	return &model.Task{
		Name:            name,
		Kind:            model.KindService,
		Run:             "sleep 1",
		Restart:         model.RestartAlways,
		MaxConcurrent:   1,
		OnOverlap:       model.PolicySkip,
		Instances:       1,
		Autostart:       true,
		HealthyAfter:    healthyAfter,
		RestartAttempts: 3,
		RestartBackoff:  model.BackoffConstant,
		DependsOn:       dependsOn,
	}
}

func activeCount(tm runtime.TaskManager, name string) int {
	return len(tm.GetActiveRuns(name))
}

// TestStartServiceInstances_HonorsAutostart proves the boot launcher starts an
// autostart service but leaves a non-autostart one stopped. This is the
// invariant that lets initDaemonServices run the launcher unconditionally (in
// cloud mode too): a non-autostart service boots stopped because its supervisor
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
// is still short of its healthy_after, then comes up once db crosses it.
func TestStartServiceInstances_GatesDependentOnDependencyHealth(t *testing.T) {
	exec := testutil.NewGateExecutor()
	bus := events.NewEventBus()
	tm := runtime.NewTaskManager(exec, bus, time.Now)
	t.Cleanup(tm.Shutdown)

	db := bootTestService("db", 400*time.Millisecond)
	web := bootTestService("web", time.Nanosecond, "db")
	tm.UpsertTask(db)
	tm.UpsertTask(web)
	tasksMap := map[string]*model.Task{"db": db, "web": web}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	startServiceInstances(ctx, tm, tasksMap)

	// db comes up immediately; web is gated behind db's 400ms healthy bar.
	require.Eventually(t, func() bool { return activeCount(tm, "db") == 1 },
		time.Second, 10*time.Millisecond, "db should start right away")
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, 0, activeCount(tm, "web"), "web must wait until db is healthy")

	require.Eventually(t, func() bool { return activeCount(tm, "web") == 1 },
		2*time.Second, 10*time.Millisecond, "web should start once db is healthy")
}

// TestPreStopServices_StopsDependentBeforeDependency proves reverse-dependency
// teardown: web (the dependent) is fully drained before db (its dependency) is
// stopped.
func TestPreStopServices_StopsDependentBeforeDependency(t *testing.T) {
	exec := testutil.NewGateExecutor()
	bus := events.NewEventBus()
	tm := runtime.NewTaskManager(exec, bus, time.Now)
	t.Cleanup(tm.Shutdown)

	// Record the order in which services report a terminal (stopped) run.
	var (
		mu          sync.Mutex
		stoppedSeq  []string
		stoppedSeen = make(map[string]bool)
	)
	bus.SubscribeAll(func(e events.Event) {
		re, ok := e.Data.(events.RunEvent)
		if !ok || re.Run == nil {
			return
		}
		if e.Type != events.EventRunCompleted && e.Type != events.EventRunFailed {
			return
		}
		mu.Lock()
		if !stoppedSeen[re.Run.TaskName] {
			stoppedSeen[re.Run.TaskName] = true
			stoppedSeq = append(stoppedSeq, re.Run.TaskName)
		}
		mu.Unlock()
	})

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

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, []string{"web", "db"}, stoppedSeq,
		"the dependent must stop before the dependency")
}
