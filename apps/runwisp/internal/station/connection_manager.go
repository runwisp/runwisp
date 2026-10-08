// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"fmt"
	"sync"
)

// connectionManager owns the active WebSocket session and outbound message
// dispatch.
type connectionManager struct {
	tracker *ExecutionTracker

	mu      sync.Mutex
	session *wsSession // nil while disconnected
}

func newConnectionManager(tracker *ExecutionTracker) *connectionManager {
	return &connectionManager{tracker: tracker}
}

// attachSession sets the active session, reporting whether the connection was
// previously detached.
func (cm *connectionManager) attachSession(s *wsSession) (isFirstConnect bool) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	isFirstConnect = cm.session == nil
	cm.session = s
	return isFirstConnect
}

func (cm *connectionManager) detachSession() {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.session = nil
}

// sendIfReady sends a message on the current active session.
func (cm *connectionManager) sendIfReady(message any) error {
	cm.mu.Lock()
	s := cm.session
	cm.mu.Unlock()

	if s == nil {
		return fmt.Errorf("not connected")
	}
	return sendMessage(s, message)
}

func (cm *connectionManager) flushPendingUpdates() {
	cm.mu.Lock()
	s := cm.session
	cm.mu.Unlock()

	if s == nil {
		return
	}

	cm.tracker.FlushPending(func(msg any) error {
		return sendMessage(s, msg)
	})
}
