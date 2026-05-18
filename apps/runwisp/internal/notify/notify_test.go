// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/events"
)

// TestServiceStop_FastWhenIdle exercises the shutdown path. With the retention
// loop wired to the same context as the dispatcher it would block until the
// caller-supplied deadline expired every time, because runRetention had no
// happy-path exit signal — its goroutine kept the WaitGroup pinned, and Stop
// only escaped via the timeout branch.
func TestServiceStop_FastWhenIdle(t *testing.T) {
	svc := New(Config{
		Bus:            events.NewEventBus(),
		Channels:       nil,
		Rules:          nil,
		FailureSink:    nil,
		RetentionEvery: time.Hour,
		RetentionFn:    func() {},
	})

	require.NoError(t, svc.Start(context.Background()))

	deadline := 2 * time.Second
	stopCtx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	start := time.Now()
	require.NoError(t, svc.Stop(stopCtx))
	elapsed := time.Since(start)

	assert.Less(t, elapsed, deadline/2,
		"Stop must return promptly when there's no work to drain; took %s of %s",
		elapsed, deadline)
}

// TestServiceStop_RetentionTickerExits ensures the retention goroutine actually
// returns rather than leaking. We invoke Stop with a generous deadline and
// verify it doesn't hit the timeout branch (where ctx is cancelled).
func TestServiceStop_RetentionTickerExits(t *testing.T) {
	var ticks atomic.Int32
	svc := New(Config{
		Bus:            events.NewEventBus(),
		RetentionEvery: 10 * time.Millisecond,
		RetentionFn:    func() { ticks.Add(1) },
	})

	require.NoError(t, svc.Start(context.Background()))

	require.Eventually(t, func() bool { return ticks.Load() > 0 }, time.Second, 5*time.Millisecond,
		"retention loop should fire while the service is running")

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, svc.Stop(stopCtx))

	assert.NoError(t, stopCtx.Err(), "Stop must not consume its own deadline when idle")

	prior := ticks.Load()
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, prior, ticks.Load(), "retention loop must stop ticking after Stop returns")
}
