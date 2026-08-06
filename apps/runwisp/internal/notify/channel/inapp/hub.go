// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package inapp is the in-app notification sink: it ingests events from the
// notify dispatcher (and synthesized delivery-failure events from the
// failure-sink path), runs them through the Coalescer, persists rows, and
// fans out via the Hub to TUI / Web UI subscribers.
package inapp

import (
	"sync"

	"github.com/runwisp/runwisp/internal/storage"
)

// Update is the SSE-shaped envelope the Hub broadcasts. Type is one of
// "notification.created", "notification.updated", or
// UpdateTypeUnreadCountChanged; Notification is the row in its current state
// (zero-valued for unread-count-only updates). UnreadCount is the database's
// post-mutation count of rows with read_at IS NULL — producers populate it so
// the SSE handler ships an authoritative number with every event.
type Update struct {
	Type         string
	Notification storage.Notification
	UnreadCount  int64
}

// SSE event type constants. Kept here so producers and the server-side
// serializer agree on the same strings.
const (
	UpdateTypeCreated            = "notification.created"
	UpdateTypeUpdated            = "notification.updated"
	UpdateTypeUnreadCountChanged = "notifications.unread_count_changed"
)

// Subscriber is the receiving end of a Hub subscription.
type Subscriber struct {
	ch chan Update
}

// Channel returns the underlying channel for select statements.
func (s *Subscriber) Channel() <-chan Update { return s.ch }

// Hub is an in-memory pub/sub for SSE consumers. Drop-oldest under
// backpressure: if a slow subscriber fills its buffered channel, the new
// update is silently dropped. SSE is a best-effort surface; pages reload via
// REST list to recover.
type Hub struct {
	mu      sync.RWMutex
	subs    map[*Subscriber]struct{}
	bufSize int
}

// NewHub constructs a Hub. bufSize is the per-subscriber channel capacity;
// 32 is a sensible default for the SSE handler.
func NewHub(bufSize int) *Hub {
	if bufSize <= 0 {
		bufSize = 32
	}
	return &Hub{
		subs:    make(map[*Subscriber]struct{}),
		bufSize: bufSize,
	}
}

// Subscribe registers a subscriber and returns an unsubscribe func.
func (h *Hub) Subscribe() (*Subscriber, func()) {
	s := &Subscriber{ch: make(chan Update, h.bufSize)}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s, func() {
		h.mu.Lock()
		delete(h.subs, s)
		close(s.ch)
		h.mu.Unlock()
	}
}

// Publish broadcasts an update to all subscribers. Drop-oldest semantics: a
// subscriber whose channel is full silently misses this update.
//
// The send happens under the read lock so it is mutually exclusive with the
// exclusive-locked unsubscribe that closes the channel — otherwise a send could
// land on an already-closed channel and panic (a send on a closed channel is
// not saved by the select's default case). The send is non-blocking, so holding
// the read lock never stalls: concurrent Publish calls are still allowed.
func (h *Hub) Publish(u Update) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for s := range h.subs {
		select {
		case s.ch <- u:
		default:
		}
	}
}
