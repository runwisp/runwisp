// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cloud

import (
	"fmt"
	"sync"

	"log/slog"
)

// connectionManager manages the WebSocket session state, lifecycle transitions,
// and outbound message dispatch. Extracted from Client to separate connection
// state from business logic.
type connectionManager struct {
	tracker *ExecutionTracker

	mu           sync.Mutex
	state        LifecycleState
	connectionID string
	session      *wsSession
	ready        bool
}

func newConnectionManager(tracker *ExecutionTracker) *connectionManager {
	return &connectionManager{
		tracker: tracker,
		state:   StateBoot,
	}
}

func (cm *connectionManager) setState(next LifecycleState) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.transitionStateLocked(next)
}

func (cm *connectionManager) transitionStateLocked(next LifecycleState) {
	if cm.state == next {
		return
	}
	previous := cm.state
	cm.state = next
	slog.Info(
		"lifecycle transition",
		"from", previous,
		"to", next,
		"connectionId", cm.connectionID,
	)
}

func (cm *connectionManager) applyExecutionStateLocked() {
	if !cm.ready {
		return
	}
	if cm.tracker.HasActive() {
		cm.transitionStateLocked(StateExecuting)
		return
	}
	cm.transitionStateLocked(StateReady)
}

func (cm *connectionManager) refreshExecutionState() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.applyExecutionStateLocked()
}

// attachSession sets the active session and transitions to ready state,
// reporting whether this is the first time the session has gone ready.
func (cm *connectionManager) attachSession(s *wsSession) (isFirstConnect bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	isFirstConnect = !cm.ready
	cm.session = s
	cm.ready = true
	cm.applyExecutionStateLocked()
	return isFirstConnect
}

func (cm *connectionManager) detachSession() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.ready = false
	cm.session = nil
	cm.connectionID = ""
}

func (cm *connectionManager) setConnectionID(id string) {
	cm.mu.Lock()
	cm.connectionID = id
	cm.mu.Unlock()
}

// sendIfReady sends a message on the current active session.
func (cm *connectionManager) sendIfReady(message any) error {
	cm.mu.Lock()
	s := cm.session
	r := cm.ready
	cm.mu.Unlock()

	if !r || s == nil {
		return fmt.Errorf("not connected")
	}
	return sendMessage(s, message)
}

func (cm *connectionManager) flushPendingUpdates() {
	cm.mu.Lock()
	s := cm.session
	r := cm.ready
	cm.mu.Unlock()

	if !r || s == nil {
		return
	}

	cm.tracker.FlushPending(func(msg any) error {
		return sendMessage(s, msg)
	})
}
