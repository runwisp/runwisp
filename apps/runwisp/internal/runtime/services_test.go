// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"sync"
	"sync/atomic"
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
		Name:          name,
		Kind:          model.KindService,
		Run:           "echo hi",
		Restart:       model.RestartAlways,
		MaxConcurrent: 1,
		OnOverlap:     model.PolicySkip,
		Instances:     instances,
		Autostart:     true,
		RestartDelay:  time.Millisecond,
		// A tiny healthy_after means every test exit clears the healthy bar and
		// counts as a successful start — these helpers model services that come
		// up fine and later exit (refill), not ones that fail to start (FATAL).
		// Tests exercising FATAL or backoff accumulation set HealthyAfter /
		// RestartAttempts explicitly.
		HealthyAfter:    time.Nanosecond,
		RestartAttempts: 3,
		RestartBackoff:  model.BackoffConstant,
	}
}

func TestStartServiceInstances(t *testing.T) {
	djm, _, eb := newGatedManager(t)
	jm := TaskManager(djm)

	task := serviceTask("svc", 3)
	jm.UpsertTask(task)

	// The gated executor keeps each instance running until cancelled, so all
	// three are observably concurrent once their start events have fired.
	started := watchRuns(eb, events.EventRunStarted)
	require.NoError(t, jm.StartServiceInstances("svc", model.TriggeredByService))
	started.waitFor(t, 3)

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
		assert.Equal(t, model.TriggeredByService, ar.Run.TriggeredBy,
			"supervisor-driven start must label runs as TriggeredByService")
	}
	djm.mu.RUnlock()
	assert.Equal(t, map[int]bool{0: true, 1: true, 2: true}, indexes)
}

func TestServiceInstanceRefillsOnExit(t *testing.T) {
	djm, exec, eb := newTestManager(t)
	jm := TaskManager(djm)

	task := serviceTask("svc", 2)
	jm.UpsertTask(task)

	// Both instances finish quickly and trigger restart. The supervisor must
	// refill the same instance index, not allocate a new one.
	exec.On("Execute", mock.Anything, task, mock.Anything).Return(
		&executor.ExecuteResult{ExitCode: 1}, 10*time.Millisecond,
	)

	// Waiting for more starts than the instance count proves refills fired,
	// without guessing how long several restart cycles take.
	started := watchRuns(eb, events.EventRunStarted)
	require.NoError(t, jm.StartServiceInstances("svc", model.TriggeredByService))
	started.waitFor(t, 4)

	djm.mu.RLock()
	ts := djm.tasks["svc"]
	// At any point in time the supervisor should target exactly task.Instances
	// live instances; the indexes must always be {0, 1}.
	for _, ar := range ts.active {
		idx := ar.Run.InstanceIndex
		assert.Truef(t, idx == 0 || idx == 1, "unexpected instance index %d", idx)
		assert.Equal(t, model.TriggeredByService, ar.Run.TriggeredBy,
			"supervisor-driven refills must label runs as TriggeredByService, not inherit")
	}
	djm.mu.RUnlock()
}

func TestServiceShutdownStopsRestarts(t *testing.T) {
	djm, exec, eb := newTestManager(t)
	jm := TaskManager(djm)

	task := serviceTask("svc", 1)
	jm.UpsertTask(task)

	exec.On("Execute", mock.Anything, task, mock.Anything).Return(
		&executor.ExecuteResult{ExitCode: 1}, 10*time.Millisecond,
	)

	started := watchRuns(eb, events.EventRunStarted)
	require.NoError(t, jm.StartServiceInstances("svc", model.TriggeredByService))
	started.waitFor(t, 2) // initial run plus at least one restart

	jm.Shutdown()

	// Shutdown waits for every execute/restart goroutine to exit, so no further
	// start can fire afterwards — the count must hold steady.
	settled := started.count()
	require.Never(t, func() bool {
		return started.count() > settled
	}, 100*time.Millisecond, 10*time.Millisecond, "no restarts after shutdown")
}

func TestServiceLoadPendingRunsSkipsServices(t *testing.T) {
	djm, _, _ := newTestManager(t)
	jm := TaskManager(djm)

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
	djm, _, eb := newGatedManager(t)
	jm := TaskManager(djm)

	task := serviceTask("svc", 3)
	jm.UpsertTask(task)

	started := watchRuns(eb, events.EventRunStarted)
	require.NoError(t, jm.StartServiceInstances("svc", model.TriggeredByService))
	started.waitFor(t, 3)

	djm.mu.RLock()
	startCount := len(djm.tasks["svc"].active)
	djm.mu.RUnlock()
	require.Equal(t, 3, startCount)

	require.NoError(t, jm.RestartServiceInstances("svc"))

	// Each of the three instances is cancelled and refilled; six starts total
	// proves the refills happened.
	started.waitFor(t, 6)
}

// TestServiceInstanceRefillsAfterManualStop is the regression test for the bug
// where `Stop` on a single service instance left the slot permanently empty.
// A service must self-heal: cancelling one instance must refill that same
// instance index.
func TestServiceInstanceRefillsAfterManualStop(t *testing.T) {
	djm, _, eb := newGatedManager(t)
	jm := TaskManager(djm)

	task := serviceTask("svc", 2)
	jm.UpsertTask(task)

	// The gated executor keeps instances running until cancelled.
	started := watchRuns(eb, events.EventRunStarted)
	require.NoError(t, jm.StartServiceInstances("svc", model.TriggeredByService))
	started.waitFor(t, 2)

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

	// The cancelled run drains and the supervisor refills slot 0 — the third
	// start event marks that refill.
	started.waitFor(t, 3)

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
	djm, exec, eb := newTestManager(t)
	jm := TaskManager(djm)

	task := serviceTask("svc", 1)
	task.RestartDelay = 30 * time.Millisecond
	// Pin the healthy bar high and the FATAL budget out of reach so every quick
	// exit stays a "fast failure": the backoff counter (the subject here) climbs
	// without the instance going FATAL and halting the climb.
	task.HealthyAfter = time.Hour
	task.RestartAttempts = 1_000_000
	jm.UpsertTask(task)

	// Each instance run exits quickly with failure; well under the healthy_after
	// threshold so the counter must keep climbing.
	exec.On("Execute", mock.Anything, task, mock.Anything).Return(
		&executor.ExecuteResult{ExitCode: 1}, 5*time.Millisecond,
	)

	// Three starts = initial + two restarts, so the attempt counter must have
	// advanced past one by the time the third start fires.
	started := watchRuns(eb, events.EventRunStarted)
	require.NoError(t, jm.StartServiceInstances("svc", model.TriggeredByService))
	started.waitFor(t, 3)

	djm.mu.RLock()
	attempts := djm.tasks["svc"].supervisor.Attempts(0)
	djm.mu.RUnlock()

	assert.Greater(t, attempts, 1,
		"counter should accumulate across multiple quick exits, got %d", attempts)
}

// TestServiceFatalAfterStartRetries is the bug-first guard for B3: a service
// that can never stay up must reach a loud terminal FATAL state instead of
// flapping forever (which is what happens without restart_attempts). It asserts
// the supervisor gives up after restart_attempts+1 fast failures, stops refilling,
// records the give-up as a start_failed run, and fires a single service.fatal
// event — then an operator restart re-attempts with a fresh budget.
func TestServiceFatalAfterStartRetries(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)
	defer jm.Shutdown()

	var (
		mu          sync.Mutex
		fatalEvents []events.ServiceFatalEvent
		startFailed int
	)
	eb.SubscribeAll(func(e events.Event) {
		mu.Lock()
		defer mu.Unlock()
		switch d := e.Data.(type) {
		case events.ServiceFatalEvent:
			fatalEvents = append(fatalEvents, d)
		case events.RunEvent:
			if d.Run != nil && d.Run.EndReason != nil && *d.Run.EndReason == model.ReasonStartFailed {
				startFailed++
			}
		}
	})

	task := serviceTask("flapper", 1)
	task.RestartAttempts = 2
	task.HealthyAfter = 2 * time.Second // every quick exit is a "fast failure"
	task.RestartDelay = time.Millisecond
	jm.UpsertTask(task)

	// Each run fails fast, well under healthy_after. Count executions via an
	// atomic counter: reading testify's exec.Calls slice directly races with the
	// restart goroutine appending to it inside MethodCalled.
	var execCalls atomic.Int64
	exec.On("Execute", mock.Anything, task, mock.Anything).
		Run(func(mock.Arguments) { execCalls.Add(1) }).
		Return(&executor.ExecuteResult{ExitCode: 1}, 5*time.Millisecond)

	require.NoError(t, jm.StartServiceInstances("flapper", model.TriggeredByService))

	djm := jm.(*defaultTaskManager)
	require.Eventually(t, func() bool {
		djm.mu.RLock()
		defer djm.mu.RUnlock()
		return djm.tasks["flapper"].supervisor.IsFatal(0)
	}, 2*time.Second, 5*time.Millisecond, "service must reach FATAL, not flap forever")

	djm.mu.RLock()
	active := len(djm.tasks["flapper"].active)
	djm.mu.RUnlock()
	assert.Equal(t, 0, active, "a FATAL service holds no live instances")

	// The FATAL state must surface in the cloud snapshot, not masquerade as
	// "degraded" (which would tell the cloud the daemon is still retrying).
	snap, ok := djm.ServiceSnapshot("flapper")
	require.True(t, ok)
	assert.Equal(t, model.ServiceFatal, snap.State)
	assert.Equal(t, 0, snap.RunningInstances)
	require.Len(t, snap.Instances, 1)
	assert.Equal(t, model.ServiceInstanceFatal, snap.Instances[0].State)

	callsAtFatal := execCalls.Load()
	assert.Equal(t, int64(3), callsAtFatal, "restart_attempts=2 → 3 fast failures, then give up")
	time.Sleep(150 * time.Millisecond)
	assert.Equal(t, callsAtFatal, execCalls.Load(), "no restarts after FATAL")

	mu.Lock()
	require.Len(t, fatalEvents, 1, "exactly one service.fatal event")
	assert.Equal(t, "flapper", fatalEvents[0].TaskName)
	assert.Equal(t, 0, fatalEvents[0].InstanceIndex)
	assert.Equal(t, 3, fatalEvents[0].Attempts)
	assert.Equal(t, 1, fatalEvents[0].LastExitCode)
	assert.Equal(t, 1, startFailed, "the give-up is recorded as a start_failed run row")
	mu.Unlock()

	// An operator restart clears FATAL and re-attempts with a fresh budget.
	require.NoError(t, jm.RestartServiceInstances("flapper"))
	require.Eventually(t, func() bool {
		return execCalls.Load() > callsAtFatal
	}, time.Second, 5*time.Millisecond, "restart must re-attempt a FATAL service")
}

func TestRestartServiceInstancesRejectsNonService(t *testing.T) {
	djm, _, _ := newTestManager(t)
	jm := TaskManager(djm)

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

// TestAutostartFalseBootsStopped proves an autostart=false service stays at
// zero instances when the boot path calls StartServiceInstances, while an
// autostart=true sibling comes up — and that an explicit Restart starts the
// stopped one on demand.
func TestAutostartFalseBootsStopped(t *testing.T) {
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)
	defer jm.Shutdown()

	manual := serviceTask("manual", 1)
	manual.Autostart = false
	auto := serviceTask("auto", 1)

	jm.UpsertTask(manual)
	jm.UpsertTask(auto)

	exec.On("Execute", mock.Anything, manual, mock.Anything).Return(
		&executor.ExecuteResult{ExitCode: -1, Stopped: true}, 200*time.Millisecond,
	)
	exec.On("Execute", mock.Anything, auto, mock.Anything).Return(
		&executor.ExecuteResult{ExitCode: -1, Stopped: true}, 200*time.Millisecond,
	)

	// Boot: the daemon calls StartServiceInstances for every service.
	require.NoError(t, jm.StartServiceInstances("manual", model.TriggeredByService))
	require.NoError(t, jm.StartServiceInstances("auto", model.TriggeredByService))
	time.Sleep(50 * time.Millisecond)

	djm := jm.(*defaultTaskManager)
	djm.mu.RLock()
	manualActive := len(djm.tasks["manual"].active)
	autoActive := len(djm.tasks["auto"].active)
	djm.mu.RUnlock()
	assert.Equal(t, 0, manualActive, "autostart=false service must not start at boot")
	assert.Equal(t, 1, autoActive, "autostart=true service starts at boot")

	// Operator starts the manual service explicitly.
	require.NoError(t, jm.RestartServiceInstances("manual"))
	time.Sleep(50 * time.Millisecond)

	djm.mu.RLock()
	manualActive = len(djm.tasks["manual"].active)
	djm.mu.RUnlock()
	assert.Equal(t, 1, manualActive, "Restart starts an autostart=false service on demand")
}

// TestStopServiceHaltsRestarts verifies that operator-initiated Stop prevents
// the supervisor from refilling instance slots until Restart is called.
func TestStopServiceHaltsRestarts(t *testing.T) {
	djm, _, eb := newGatedManager(t)
	jm := TaskManager(djm)

	task := serviceTask("svc", 2)
	jm.UpsertTask(task)

	started := watchRuns(eb, events.EventRunStarted)
	done := watchCompletions(eb)
	require.NoError(t, jm.StartServiceInstances("svc", model.TriggeredByService))
	started.waitFor(t, 2)

	require.NoError(t, jm.StopService("svc"))

	// Stop cancels both instances; once they exit, the supervisor must not
	// refill them while the stop flag is set.
	done.waitFor(t, 2)
	require.Never(t, func() bool {
		djm.mu.RLock()
		defer djm.mu.RUnlock()
		return len(djm.tasks["svc"].active) != 0
	}, 100*time.Millisecond, 10*time.Millisecond, "no instances should be live after Stop")

	// Restart clears the flag and brings instances back.
	require.NoError(t, jm.RestartServiceInstances("svc"))
	require.Eventually(t, func() bool {
		djm.mu.RLock()
		defer djm.mu.RUnlock()
		return len(djm.tasks["svc"].active) == 2
	}, 2*time.Second, 10*time.Millisecond, "Restart should bring instances back")
}
