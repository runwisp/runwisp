// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func serviceTask(name string, instances int) *model.Task {
	return &model.Task{
		Name:           name,
		Kind:           model.KindService,
		Run:            "echo hi",
		Restart:        model.RestartAlways,
		MaxConcurrent:  1,
		OnOverlap:      model.PolicySkip,
		Instances:      instances,
		RestartDelay:   time.Millisecond,
		RestartBackoff: model.BackoffConstant,
	}
}

func TestStartServiceInstances(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)
	defer jm.Shutdown()

	task := serviceTask("svc", 3)
	jm.UpsertTask(task)

	// Block each instance long enough to observe them all running concurrently.
	exec.On("Execute", mock.Anything, task, mock.Anything).Return(
		&executor.ExecuteResult{ExitCode: -1, Stopped: true}, 500*time.Millisecond,
	)

	require.NoError(t, jm.StartServiceInstances("svc"))

	time.Sleep(50 * time.Millisecond)

	djm := jm.(*defaultTaskManager)
	djm.mu.RLock()
	ts := djm.tasks["svc"]
	assert.Len(t, ts.active, 3, "all 3 instances should be active")
	assert.Equal(t, 3, ts.supervisor.LiveCount())
	for i := 0; i < 3; i++ {
		assert.True(t, ts.supervisor.IsLive(i), "instance %d should be live", i)
	}
	indexes := make(map[int]bool)
	for _, ar := range ts.active {
		indexes[ar.Run.InstanceIndex] = true
	}
	djm.mu.RUnlock()
	assert.Equal(t, map[int]bool{0: true, 1: true, 2: true}, indexes)
}

func TestServiceInstanceRefillsOnExit(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)
	defer jm.Shutdown()

	task := serviceTask("svc", 2)
	jm.UpsertTask(task)

	// Both instances finish quickly and trigger restart. The supervisor must
	// refill the same instance index, not allocate a new one.
	exec.On("Execute", mock.Anything, task, mock.Anything).Return(
		&executor.ExecuteResult{ExitCode: 1}, 10*time.Millisecond,
	)

	require.NoError(t, jm.StartServiceInstances("svc"))

	// Allow several restart cycles.
	time.Sleep(200 * time.Millisecond)

	djm := jm.(*defaultTaskManager)
	djm.mu.RLock()
	ts := djm.tasks["svc"]
	// At any point in time the supervisor should target exactly task.Instances
	// live instances; the indexes must always be {0, 1}.
	for _, ar := range ts.active {
		idx := ar.Run.InstanceIndex
		assert.Truef(t, idx == 0 || idx == 1, "unexpected instance index %d", idx)
	}
	djm.mu.RUnlock()

	// Many Execute calls should have fired due to repeated restarts.
	assert.Greater(t, len(exec.Calls), 2)
}

func TestServiceShutdownStopsRestarts(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)

	task := serviceTask("svc", 1)
	jm.UpsertTask(task)

	exec.On("Execute", mock.Anything, task, mock.Anything).Return(
		&executor.ExecuteResult{ExitCode: 1}, 10*time.Millisecond,
	)

	require.NoError(t, jm.StartServiceInstances("svc"))
	time.Sleep(50 * time.Millisecond)

	jm.Shutdown()

	// Capture call count immediately after shutdown returned.
	callsAtShutdown := len(exec.Calls)

	// Wait past several would-be restart intervals; no new calls should appear.
	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, callsAtShutdown, len(exec.Calls), "no restarts after shutdown")
}

func TestServiceLoadPendingRunsSkipsServices(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)
	defer jm.Shutdown()

	task := serviceTask("svc", 2)
	jm.UpsertTask(task)

	pending := []model.Run{
		{ID: "01", TaskName: "svc", Status: model.PhasePending},
		{ID: "02", TaskName: "svc", Status: model.PhasePending},
	}
	result := jm.LoadPendingRuns(pending)
	assert.Equal(t, 0, result.Resumed)
	assert.Equal(t, 0, result.Queued)
	assert.Equal(t, 2, result.Skipped, "service pending runs are skipped, not resumed")
}

func TestRestartServiceInstancesCancelsAll(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)
	defer jm.Shutdown()

	task := serviceTask("svc", 3)
	jm.UpsertTask(task)

	exec.On("Execute", mock.Anything, task, mock.Anything).Return(
		&executor.ExecuteResult{ExitCode: -1, Stopped: true}, 200*time.Millisecond,
	)

	require.NoError(t, jm.StartServiceInstances("svc"))
	time.Sleep(30 * time.Millisecond)

	djm := jm.(*defaultTaskManager)
	djm.mu.RLock()
	startCount := len(djm.tasks["svc"].active)
	djm.mu.RUnlock()
	require.Equal(t, 3, startCount)

	require.NoError(t, jm.RestartServiceInstances("svc"))

	// Instances should drain (Stopped) then refill via supervisor.
	time.Sleep(300 * time.Millisecond)

	// More than 3 Execute calls means refills happened.
	assert.GreaterOrEqual(t, len(exec.Calls), 6)
}

// TestServiceInstanceRefillsAfterManualStop is the regression test for the bug
// where `Stop` on a single service instance left the slot permanently empty.
// A service must self-heal: cancelling one instance must refill that same
// instance index.
func TestServiceInstanceRefillsAfterManualStop(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)
	defer jm.Shutdown()

	task := serviceTask("svc", 2)
	jm.UpsertTask(task)

	// Long block — instances only end via context cancellation.
	exec.On("Execute", mock.Anything, task, mock.Anything).Return(
		&executor.ExecuteResult{ExitCode: -1, Stopped: true}, 5*time.Second,
	)

	require.NoError(t, jm.StartServiceInstances("svc"))
	time.Sleep(50 * time.Millisecond)

	djm := jm.(*defaultTaskManager)
	djm.mu.RLock()
	require.Len(t, djm.tasks["svc"].active, 2, "both instances should be running")
	var targetRunID string
	for _, ar := range djm.tasks["svc"].active {
		if ar.Run.InstanceIndex == 0 {
			targetRunID = ar.Run.ID
			break
		}
	}
	djm.mu.RUnlock()
	require.NotEmpty(t, targetRunID, "instance index 0 must be active")

	require.NoError(t, jm.TerminateRun(targetRunID))

	// Wait for the cancelled run to drain and the supervisor to refill the slot.
	time.Sleep(150 * time.Millisecond)

	djm.mu.RLock()
	defer djm.mu.RUnlock()

	assert.Len(t, djm.tasks["svc"].active, 2, "supervisor should refill instance 0")
	assert.True(t, djm.tasks["svc"].supervisor.IsLive(0), "instance index 0 should be live again")

	var refillRunID string
	for _, ar := range djm.tasks["svc"].active {
		if ar.Run.InstanceIndex == 0 {
			refillRunID = ar.Run.ID
			break
		}
	}
	assert.NotEqual(t, targetRunID, refillRunID, "a fresh run should occupy instance 0")
}

// TestRestartAttemptsIncrementOnQuickExit verifies that the per-instance
// restart-attempt counter advances on every short-lived exit. The state
// itself lives on the supervisor; this exercise pins the integration with
// the manager's run-completion path.
func TestRestartAttemptsIncrementOnQuickExit(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)
	defer jm.Shutdown()

	task := serviceTask("svc", 1)
	// Long enough delay between restarts that we can sample mid-cycle without
	// races, but short enough to keep the test quick.
	task.RestartDelay = 30 * time.Millisecond
	jm.UpsertTask(task)

	// Each instance run exits quickly with failure; well under the 60s reset
	// threshold so the counter must keep climbing.
	exec.On("Execute", mock.Anything, task, mock.Anything).Return(
		&executor.ExecuteResult{ExitCode: 1}, 5*time.Millisecond,
	)

	require.NoError(t, jm.StartServiceInstances("svc"))
	time.Sleep(250 * time.Millisecond)

	djm := jm.(*defaultTaskManager)
	djm.mu.RLock()
	attempts := djm.tasks["svc"].supervisor.Attempts(0)
	djm.mu.RUnlock()

	assert.Greater(t, attempts, 1,
		"counter should accumulate across multiple quick exits, got %d", attempts)
}

func TestRestartServiceInstancesRejectsNonService(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)
	defer jm.Shutdown()

	jm.UpsertTask(&model.Task{
		Name:          "cron-job",
		Run:           "echo hi",
		MaxConcurrent: 1,
		OnOverlap:     model.PolicySkip,
	})

	err := jm.RestartServiceInstances("cron-job")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a service")
}

// TestStopServiceHaltsRestarts verifies that operator-initiated Stop prevents
// the supervisor from refilling instance slots until Restart is called.
func TestStopServiceHaltsRestarts(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)
	defer jm.Shutdown()

	task := serviceTask("svc", 2)
	jm.UpsertTask(task)

	exec.On("Execute", mock.Anything, task, mock.Anything).Return(
		&executor.ExecuteResult{ExitCode: -1, Stopped: true}, 200*time.Millisecond,
	)

	require.NoError(t, jm.StartServiceInstances("svc"))
	time.Sleep(50 * time.Millisecond)

	require.NoError(t, jm.StopService("svc"))

	// Give plenty of time past the would-be restart interval; the supervisor
	// must not refill slots while the stop flag is set.
	time.Sleep(400 * time.Millisecond)

	djm := jm.(*defaultTaskManager)
	djm.mu.RLock()
	active := len(djm.tasks["svc"].active)
	djm.mu.RUnlock()
	assert.Equal(t, 0, active, "no instances should be live after Stop")

	callsAtStop := len(exec.Calls)
	time.Sleep(200 * time.Millisecond)
	assert.Equal(t, callsAtStop, len(exec.Calls), "no restarts while stopped")

	// Restart clears the flag and brings instances back.
	require.NoError(t, jm.RestartServiceInstances("svc"))
	time.Sleep(150 * time.Millisecond)

	djm.mu.RLock()
	active = len(djm.tasks["svc"].active)
	djm.mu.RUnlock()
	assert.Equal(t, 2, active, "Restart should bring instances back")
}
