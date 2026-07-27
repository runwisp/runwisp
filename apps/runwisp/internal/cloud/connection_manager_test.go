// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cloud

import (
	"testing"

	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectionManager_SendIfReady_NotConnected(t *testing.T) {
	cm := newConnectionManager(NewExecutionTracker())
	err := cm.sendIfReady("test-message")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not connected")
}

// TestConnectionManager_StateTransitions drives the lifecycle through its
// public mutators (attachSession to go ready; tracker.TrackRunning to mark an
// active execution) and observes the resulting state. The state field is
// package-private with no getter — this test is the only seam available, so
// it intentionally inspects cm.state directly.
func TestConnectionManager_StateTransitions(t *testing.T) {
	t.Run("attachSession-with-no-active-executions-becomes-ready", func(t *testing.T) {
		cm := newConnectionManager(NewExecutionTracker())
		cm.attachSession(&wsSession{}) // exercises real ready-transition path
		assert.Equal(t, StateReady, cm.state)
	})

	t.Run("active-execution-flips-state-to-executing", func(t *testing.T) {
		tracker := NewExecutionTracker()
		cm := newConnectionManager(tracker)
		cm.attachSession(&wsSession{})
		tracker.TrackRunning("exec-1", nil)
		cm.refreshExecutionState()
		assert.Equal(t, StateExecuting, cm.state)
	})

	t.Run("detachSession-clears-ready", func(t *testing.T) {
		cm := newConnectionManager(NewExecutionTracker())
		cm.attachSession(&wsSession{})
		cm.detachSession()
		// After detach, sendIfReady must fail with not-connected — a public
		// behavior signal that the ready flag was cleared.
		err := cm.sendIfReady("x")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not connected")
	})
}

// flushPendingUpdates must early-return when there is no active session so
// the tracker doesn't try to serialise updates onto a nil websocket.
func TestConnectionManager_FlushPendingUpdates_NotReadyIsNoop(t *testing.T) {
	tracker := NewExecutionTracker()
	cm := newConnectionManager(tracker)

	tracker.QueueUpdate(NewExecutionUpdateMessage("exec-1", protocol.ExecutionStatusOk, nil, nil, nil), nil)

	// Not ready — must not panic, must not flush.
	cm.flushPendingUpdates()

	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	assert.Len(t, tracker.pendingExecutionUpdates, 1,
		"flushPendingUpdates must not drain the buffer when the session is detached")
}

// When a session is attached, flushPendingUpdates routes buffered updates
// through the live session's outbound channel.
func TestConnectionManager_FlushPendingUpdates_DrainsThroughSession(t *testing.T) {
	tracker := NewExecutionTracker()
	cm := newConnectionManager(tracker)

	session := &wsSession{outbound: make(chan []byte, 4)}
	cm.attachSession(session)

	tracker.QueueUpdate(NewExecutionUpdateMessage("exec-1", protocol.ExecutionStatusOk, nil, nil, nil), nil)
	tracker.QueueUpdate(NewExecutionUpdateMessage("exec-2", protocol.ExecutionStatusOk, nil, nil, nil), nil)

	cm.flushPendingUpdates()

	assert.Len(t, session.outbound, 2, "both buffered updates must be queued onto the session")
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	assert.Empty(t, tracker.pendingExecutionUpdates, "tracker buffer must be drained")
}
