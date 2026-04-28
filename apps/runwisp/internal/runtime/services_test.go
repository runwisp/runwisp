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
		Parallelism:    1,
		OnOverlap:      model.PolicySkip,
		Instances:      instances,
		RestartDelay:   "1ms",
		RestartBackoff: model.RestartBackoffNone,
	}
}

func TestStartServiceReplicas(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb)
	defer jm.Shutdown()

	task := serviceTask("svc", 3)
	jm.UpsertTask(task)

	// Block each replica long enough to observe them all running concurrently.
	exec.On("Execute", mock.Anything, task, mock.Anything).Return(
		&executor.ExecuteResult{ExitCode: -1, Stopped: true}, 500*time.Millisecond,
	)

	require.NoError(t, jm.StartServiceReplicas("svc"))

	time.Sleep(50 * time.Millisecond)

	djm := jm.(*defaultTaskManager)
	djm.mu.RLock()
	ts := djm.tasks["svc"]
	assert.Len(t, ts.active, 3, "all 3 replicas should be active")
	assert.Len(t, ts.liveReplicas, 3)
	for i := 0; i < 3; i++ {
		_, ok := ts.liveReplicas[i]
		assert.True(t, ok, "replica %d should be live", i)
	}
	indexes := make(map[int]bool)
	for _, ar := range ts.active {
		indexes[ar.Run.ReplicaIndex] = true
	}
	djm.mu.RUnlock()
	assert.Equal(t, map[int]bool{0: true, 1: true, 2: true}, indexes)
}

func TestServiceReplicaRefillsOnExit(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb)
	defer jm.Shutdown()

	task := serviceTask("svc", 2)
	jm.UpsertTask(task)

	// Both replicas finish quickly and trigger restart. The supervisor must
	// refill the same replica index, not allocate a new one.
	exec.On("Execute", mock.Anything, task, mock.Anything).Return(
		&executor.ExecuteResult{ExitCode: 1}, 10*time.Millisecond,
	)

	require.NoError(t, jm.StartServiceReplicas("svc"))

	// Allow several restart cycles.
	time.Sleep(200 * time.Millisecond)

	djm := jm.(*defaultTaskManager)
	djm.mu.RLock()
	ts := djm.tasks["svc"]
	// At any point in time the supervisor should target exactly task.Instances
	// live replicas; the indexes must always be {0, 1}.
	for idx := range ts.liveReplicas {
		assert.Truef(t, idx == 0 || idx == 1, "unexpected replica index %d", idx)
	}
	djm.mu.RUnlock()

	// Many Execute calls should have fired due to repeated restarts.
	assert.Greater(t, len(exec.Calls), 2)
}

func TestServiceShutdownStopsRestarts(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb)

	task := serviceTask("svc", 1)
	jm.UpsertTask(task)

	exec.On("Execute", mock.Anything, task, mock.Anything).Return(
		&executor.ExecuteResult{ExitCode: 1}, 10*time.Millisecond,
	)

	require.NoError(t, jm.StartServiceReplicas("svc"))
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
	jm := NewTaskManager(exec, eb)
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

func TestRestartServiceReplicasCancelsAll(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb)
	defer jm.Shutdown()

	task := serviceTask("svc", 3)
	jm.UpsertTask(task)

	exec.On("Execute", mock.Anything, task, mock.Anything).Return(
		&executor.ExecuteResult{ExitCode: -1, Stopped: true}, 200*time.Millisecond,
	)

	require.NoError(t, jm.StartServiceReplicas("svc"))
	time.Sleep(30 * time.Millisecond)

	djm := jm.(*defaultTaskManager)
	djm.mu.RLock()
	startCount := len(djm.tasks["svc"].active)
	djm.mu.RUnlock()
	require.Equal(t, 3, startCount)

	require.NoError(t, jm.RestartServiceReplicas("svc"))

	// Replicas should drain (Stopped) then refill via supervisor.
	time.Sleep(300 * time.Millisecond)

	// More than 3 Execute calls means refills happened.
	assert.GreaterOrEqual(t, len(exec.Calls), 6)
}

// TestServiceReplicaRefillsAfterManualStop is the regression test for the bug
// where `Stop` on a single service replica left the slot permanently empty.
// A service must self-heal: cancelling one replica must refill that same
// replica index.
func TestServiceReplicaRefillsAfterManualStop(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb)
	defer jm.Shutdown()

	task := serviceTask("svc", 2)
	jm.UpsertTask(task)

	// Long block — replicas only end via context cancellation.
	exec.On("Execute", mock.Anything, task, mock.Anything).Return(
		&executor.ExecuteResult{ExitCode: -1, Stopped: true}, 5*time.Second,
	)

	require.NoError(t, jm.StartServiceReplicas("svc"))
	time.Sleep(50 * time.Millisecond)

	djm := jm.(*defaultTaskManager)
	djm.mu.RLock()
	require.Len(t, djm.tasks["svc"].active, 2, "both replicas should be running")
	var targetRunID string
	for _, ar := range djm.tasks["svc"].active {
		if ar.Run.ReplicaIndex == 0 {
			targetRunID = ar.Run.ID
			break
		}
	}
	djm.mu.RUnlock()
	require.NotEmpty(t, targetRunID, "replica index 0 must be active")

	require.NoError(t, jm.TerminateRun(targetRunID))

	// Wait for the cancelled run to drain and the supervisor to refill the slot.
	time.Sleep(150 * time.Millisecond)

	djm.mu.RLock()
	defer djm.mu.RUnlock()

	assert.Len(t, djm.tasks["svc"].active, 2, "supervisor should refill replica 0")
	_, slot0Live := djm.tasks["svc"].liveReplicas[0]
	assert.True(t, slot0Live, "replica index 0 should be live again")

	var refillRunID string
	for _, ar := range djm.tasks["svc"].active {
		if ar.Run.ReplicaIndex == 0 {
			refillRunID = ar.Run.ID
			break
		}
	}
	assert.NotEqual(t, targetRunID, refillRunID, "a fresh run should occupy replica 0")
}

func TestRestartServiceReplicasRejectsNonService(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb)
	defer jm.Shutdown()

	jm.UpsertTask(&model.Task{
		Name:        "cron-job",
		Run:         "echo hi",
		Parallelism: 1,
		OnOverlap:   model.PolicySkip,
	})

	err := jm.RestartServiceReplicas("cron-job")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a service")
}
