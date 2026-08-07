// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cloud

import (
	"fmt"
	"sync"
)

// connectionManager owns the active WebSocket session and outbound message
// dispatch. Extracted from Client to separate connection state from business
// logic.
type connectionManager struct {
	tracker *ExecutionTracker

	mu      sync.Mutex
	session *wsSession
	ready   bool
}

func newConnectionManager(tracker *ExecutionTracker) *connectionManager {
	return &connectionManager{tracker: tracker}
}

// attachSession sets the active session and marks the connection ready,
// reporting whether this is the first time the session has gone ready.
func (cm *connectionManager) attachSession(s *wsSession) (isFirstConnect bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	isFirstConnect = !cm.ready
	cm.session = s
	cm.ready = true
	return isFirstConnect
}

func (cm *connectionManager) detachSession() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.ready = false
	cm.session = nil
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
