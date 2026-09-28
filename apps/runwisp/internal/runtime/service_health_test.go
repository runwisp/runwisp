// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
)

// probeExecutor plays the RoutingExecutor's part in the RunWatcher contract:
// each run stays up until stopped or killed, with the manager's watcher
// running next to it and joined before the result is returned. Probes answer
// from pass, in order, then keep passing.
type probeExecutor struct {
	watcher executor.RunWatcher

	mu   sync.Mutex
	pass []bool
}

func (e *probeExecutor) SetRunWatcher(w executor.RunWatcher) { e.watcher = w }

func (e *probeExecutor) Availability() executor.Availability { return executor.Availability{} }

func (e *probeExecutor) Execute(ctx context.Context, task *model.Task, run *model.Run) *executor.ExecuteResult {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	ctl := &probeControl{e: e, cancel: cancel}
	watchCtx, stopWatch := context.WithCancel(runCtx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.watcher(watchCtx, task, run, ctl)
	}()
	<-runCtx.Done()
	stopWatch()
	<-done
	if reason := ctl.killed(); reason != "" {
		return &executor.ExecuteResult{ExitCode: -1, KillReason: reason}
	}
	return &executor.ExecuteResult{ExitCode: -1, Stopped: true}
}

type probeControl struct {
	e      *probeExecutor
	cancel context.CancelFunc

	mu     sync.Mutex
	reason model.EndReason
}

func (c *probeControl) Probe(context.Context, *model.Task) executor.ProbeResult {
	c.e.mu.Lock()
	defer c.e.mu.Unlock()
	if len(c.e.pass) > 0 {
		ok := c.e.pass[0]
		c.e.pass = c.e.pass[1:]
		if !ok {
			return executor.ProbeResult{ExecuteResult: executor.ExecuteResult{ExitCode: 1}}
		}
	}
	return executor.ProbeResult{}
}

func (c *probeControl) Annotate(string) {}

func (c *probeControl) Kill(reason model.EndReason) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.reason == "" {
		c.reason = reason
		c.cancel()
	}
}

func (c *probeControl) killed() model.EndReason {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reason
}

func newProbeManager(t *testing.T, pass ...bool) (*defaultTaskManager, *events.Bus) {
	t.Helper()
	eb := events.NewEventBus()
	jm := NewTaskManager(&probeExecutor{pass: pass}, eb, time.Now)
	t.Cleanup(jm.Shutdown)
	return jm.(*defaultTaskManager), eb
}

// checkedService is a service with a health_check on the fastest cron there
// is (@every rounds up to 1s) that never retries a failed check.
func checkedService(healthyAfter time.Duration) *model.Task {
	task := serviceTask("svc", 1)
	task.HealthyAfter = &healthyAfter
	task.HealthCheck = &model.Task{Name: "svc.health_check", Run: "check", Cron: "@every 1s"}
	return task
}

func TestServiceHealthCheck_PassMakesServiceHealthy(t *testing.T) {
	djm, eb := newProbeManager(t)
	djm.UpsertTask(checkedService(time.Hour))

	started := watchRuns(eb, events.EventRunStarted)
	require.NoError(t, djm.StartServiceInstances("svc", model.TriggeredByService))
	started.waitFor(t, 1)

	assert.False(t, djm.ServiceHealthy("svc"), "a checked instance is not healthy until its check passes")
	assert.Eventually(t, func() bool { return djm.ServiceHealthy("svc") },
		3*time.Second, 10*time.Millisecond, "the first pass makes it healthy")
}

func TestServiceHealthCheck_FailingCheckRestartsInstance(t *testing.T) {
	djm, eb := newProbeManager(t, true, false)
	djm.UpsertTask(checkedService(time.Hour))

	ended := watchRuns(eb, events.EventRunFailed)
	started := watchRuns(eb, events.EventRunStarted)
	require.NoError(t, djm.StartServiceInstances("svc", model.TriggeredByService))

	require.Eventually(t, func() bool { return ended.count() >= 1 && started.count() >= 2 },
		5*time.Second, 10*time.Millisecond, "the unhealthy instance ends and a new one starts")
	run := ended.snapshot()[0]
	require.NotNil(t, run.EndReason)
	assert.Equal(t, model.ReasonUnhealthy, *run.EndReason)
	assert.True(t, run.IsFailure)
}

func TestServiceHealthCheck_MissedDeadlineIsAFailedStart(t *testing.T) {
	djm, _ := newProbeManager(t)
	task := checkedService(200 * time.Millisecond) // the deadline passes before the first 1s tick
	task.RestartAttempts = intPtr(0)
	djm.UpsertTask(task)

	require.NoError(t, djm.StartServiceInstances("svc", model.TriggeredByService))

	require.Eventually(t, func() bool {
		djm.mu.RLock()
		defer djm.mu.RUnlock()
		return djm.tasks["svc"].supervisor.IsFatal(0)
	}, 3*time.Second, 10*time.Millisecond, "missing the deadline counts toward FATAL")
}
