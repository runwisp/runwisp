// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package inapp

import (
	"context"
	"strconv"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/storage"
)

// Announcer publishes daemon-level notifications straight to the persistent
// store (and the SSE Hub), bypassing event-bus routing and the coalescing
// window. It is the counterpart to Coalescer for facts that aren't run-derived
// and shouldn't fold or re-surface: "announce once, ever". Idempotent by
// fingerprint — announcing the same thing twice stores one row and never clears
// its read state — so a slow-moving fact like an available update, once
// dismissed, stays dismissed even across restarts.
//
// Unlike the routed in-app Channel, an Announcer works regardless of notify
// config: the server reads the notification repo unconditionally, so the row
// shows in the bell even when no route targets the in-app channel. hub may be
// nil (no live SSE); the row still persists and appears on the client's next
// REST resync.
type Announcer struct {
	repo  storage.NotificationRepository
	hub   *Hub
	clock notify.Clocker
}

// NewAnnouncer constructs an Announcer. hub may be nil.
func NewAnnouncer(repo storage.NotificationRepository, hub *Hub, clock notify.Clocker) *Announcer {
	if clock == nil {
		clock = notify.RealClock()
	}
	return &Announcer{repo: repo, hub: hub, clock: clock}
}

// Announce persists a single notification for ev (rendered as title/body) if one
// with the same fingerprint does not already exist, and — only when it was
// freshly created and a Hub is present — pushes it live over SSE. A no-op after
// the first announcement of the same fingerprint.
func (a *Announcer) Announce(ctx context.Context, ev *notify.Event, title, body string) error {
	if ev == nil {
		return nil
	}
	now := a.clock.Now()
	fp := hashFingerprint([]byte(notify.FingerprintKey(ev)))
	n := &storage.Notification{
		ID:             ulid.Make().String(),
		Fingerprint:    strconv.FormatUint(fp, 16),
		Kind:           string(ev.Kind),
		Severity:       string(ev.Severity),
		Title:          title,
		Body:           body,
		Count:          1,
		Occurrences:    []time.Time{now},
		CreatedAt:      now,
		LastOccurredAt: now,
	}
	created, err := a.repo.EnsureNotificationByFingerprint(ctx, n)
	if err != nil {
		return err
	}
	if !created || a.hub == nil {
		return nil
	}
	count, err := a.repo.CountUnreadNotifications(ctx)
	if err != nil {
		// The row is stored; SSE clients recover the count on the next event or
		// resync. -1 tells the client to ignore the count on this envelope.
		count = -1
	}
	a.hub.Publish(Update{Type: UpdateTypeCreated, Notification: *n, UnreadCount: count})
	return nil
}
