// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
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
	task.HealthyAfter = time.Hour // never crosses the bar within the test
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
