// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/executor"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/testutil"
	"github.com/runwisp/runwisp/apps/runwisp/internal/testutil/fakeclock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestWaitServiceHealthyReturnsWhenHealthy: a started service whose instance
// crosses healthy_after releases a waiter with a nil error.
func TestWaitServiceHealthyReturnsWhenHealthy(t *testing.T) {
	djm, _, eb := newGatedManager(t)
	jm := TaskManager(djm)

	task := serviceTask("svc", 1) // healthy_after = 1ns, so it's healthy as soon as it's live
	jm.UpsertTask(task)

	started := watchRuns(eb, events.EventRunStarted)
	require.NoError(t, jm.StartServiceInstances("svc", model.TriggeredByService))
	started.waitFor(t, 1)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	assert.NoError(t, jm.WaitServiceHealthy(ctx, "svc"))
}

// TestWaitServiceHealthyTimesOut: an instance that is live but never reaches its
// (long) healthy_after leaves the waiter to hit the context deadline.
func TestWaitServiceHealthyTimesOut(t *testing.T) {
	djm, _, eb := newGatedManager(t)
	jm := TaskManager(djm)

	task := serviceTask("svc", 1)
	task.HealthyAfter = durPtr(time.Hour) // never crosses the bar within the test
	jm.UpsertTask(task)

	started := watchRuns(eb, events.EventRunStarted)
	require.NoError(t, jm.StartServiceInstances("svc", model.TriggeredByService))
	started.waitFor(t, 1)

	require.False(t, jm.ServiceHealthy("svc"), "service must not be healthy yet")

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	err := jm.WaitServiceHealthy(ctx, "svc")
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "expected deadline error, got %v", err)
}

// TestWaitServiceHealthyGivesUpWhenStopped: an operator-stopped (here:
// autostart=false) service can never reach healthy, so the waiter returns an
// error early instead of burning the whole window.
func TestWaitServiceHealthyGivesUpWhenStopped(t *testing.T) {
	djm, _, _ := newTestManager(t)
	jm := TaskManager(djm)

	task := serviceTask("svc", 1)
	task.Autostart = false // boots in the stopped state
	jm.UpsertTask(task)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := jm.WaitServiceHealthy(ctx, "svc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stopped")
}

// TestWaitServiceHealthyGivesUpWhenDown: once every instance has exited with no
// restart coming (restart = "never", on_failure after a clean exit, or
// restart_attempts exhausted into FATAL), the waiter returns at once instead of
// sitting out its window. The clock is frozen, so every run lasts zero time and
// never crosses healthy_after.
func TestWaitServiceHealthyGivesUpWhenDown(t *testing.T) {
	cases := []struct {
		name     string
		restart  model.RestartPolicy
		exitCode int
	}{
		{"never", model.RestartNever, 0},
		{"on_failure after a clean exit", model.RestartOnFailure, 0},
		{"restart attempts exhausted", model.RestartAlways, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exec := new(testutil.MockExecutor)
			eb := events.NewEventBus()
			jm := NewTaskManager(exec, eb, fakeclock.New(time.Unix(0, 0)).Now)
			t.Cleanup(jm.Shutdown)

			task := serviceTask("svc", 2)
			task.Restart = tc.restart
			task.RestartAttempts = intPtr(0)
			jm.UpsertTask(task)
			exec.On("Execute", mock.Anything, task, mock.Anything).
				Return(&executor.ExecuteResult{ExitCode: tc.exitCode}, time.Millisecond)

			ended := watchCompletions(eb)
			require.NoError(t, jm.StartServiceInstances("svc", model.TriggeredByService))
			ended.waitFor(t, 2)

			// No deadline: without the early give-up this would block forever.
			done := make(chan error, 1)
			go func() { done <- jm.WaitServiceHealthy(context.Background(), "svc") }()
			select {
			case err := <-done:
				require.Error(t, err)
				assert.Contains(t, err.Error(), "down")
			case <-time.After(5 * time.Second):
				t.Fatal("WaitServiceHealthy kept waiting on a service that will never come up")
			}
		})
	}
}
