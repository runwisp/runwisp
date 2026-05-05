// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
	mu       sync.RWMutex
	subs     map[*Subscriber]struct{}
	bufSize  int
	recentID []string
	recentAt int
	maxIDs   int
}

// NewHub constructs a Hub. bufSize is the per-subscriber channel capacity;
// 32 is a sensible default for the SSE handler.
func NewHub(bufSize, recent int) *Hub {
	if bufSize <= 0 {
		bufSize = 32
	}
	if recent <= 0 {
		recent = 50
	}
	return &Hub{
		subs:     make(map[*Subscriber]struct{}),
		bufSize:  bufSize,
		recentID: make([]string, recent),
		maxIDs:   recent,
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
func (h *Hub) Publish(u Update) {
	h.mu.Lock()
	if u.Notification.ID != "" {
		h.recentID[h.recentAt%h.maxIDs] = u.Notification.ID
		h.recentAt++
	}
	subs := make([]*Subscriber, 0, len(h.subs))
	for s := range h.subs {
		subs = append(subs, s)
	}
	h.mu.Unlock()

	for _, s := range subs {
		select {
		case s.ch <- u:
		default:
		}
	}
}

// SubscriberCount reports the current subscriber count. Diagnostic-only.
func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}
