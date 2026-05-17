// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"testing"

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
