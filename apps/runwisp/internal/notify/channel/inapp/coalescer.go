// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package inapp

import (
	"context"
	"hash/fnv"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/runwisp/runwisp/apps/runwisp/internal/notify"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
)

// CoalescerConfig parameterizes the dedupe behavior.
type CoalescerConfig struct {
	Window        time.Duration // matches existing rows whose last_occurred_at falls within this span
	CoalesceLimit int           // ring size of recorded occurrence timestamps
}

// Default values for CoalescerConfig.
const (
	DefaultWindow        = time.Hour
	DefaultCoalesceLimit = 10
)

// Coalescer folds repeats into a single persistent row. SQLite is the
// authoritative store; UpsertByFingerprint owns dedupe in a single transaction
// (SELECT-or-INSERT-or-UPDATE), so the Coalescer is a thin glue layer.
type Coalescer struct {
	repo  storage.NotificationRepository
	hub   *Hub
	clock func() time.Time
	cfg   CoalescerConfig
	log   *slog.Logger

	// resetAt is when each task last succeeded (ResetTask). A closable row
	// older than that never folds a new occurrence, so a failure after a
	// success opens a fresh bell entry.
	// ponytail: in memory only, so a restart or a notify reload forgets it and
	// the next failure can still fold into the row from before the success.
	// Persist it (or look up the task's last success) if that ever matters.
	mu      sync.Mutex
	resetAt map[string]time.Time
}

// NewCoalescer constructs a coalescer.
func NewCoalescer(repo storage.NotificationRepository, hub *Hub, clock func() time.Time, cfg CoalescerConfig, log *slog.Logger) *Coalescer {
	if cfg.Window == 0 {
		cfg.Window = DefaultWindow
	}
	if cfg.CoalesceLimit == 0 {
		cfg.CoalesceLimit = DefaultCoalesceLimit
	}
	if log == nil {
		log = slog.Default()
	}
	return &Coalescer{
		repo:    repo,
		hub:     hub,
		clock:   clock,
		cfg:     cfg,
		log:     log,
		resetAt: make(map[string]time.Time),
	}
}

// ResetTask records that task just succeeded, ending its failure rows' window.
func (c *Coalescer) ResetTask(task string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resetAt[task] = c.clock()
}

// window is the span an existing row may fold ev into: the configured window,
// cut short at the task's last success for events a success closes.
func (c *Coalescer) window(ev *notify.Event, now time.Time) time.Duration {
	if !notify.ClosedBySuccess(ev) {
		return c.cfg.Window
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	reset, ok := c.resetAt[ev.TaskName]
	if !ok {
		return c.cfg.Window
	}
	return min(c.cfg.Window, now.Sub(reset))
}

// Receive folds the (rendered, ev) pair into the persistent store and
// publishes the resulting Update on the hub.
func (c *Coalescer) Receive(ctx context.Context, title, body string, ev *notify.Event) {
	now := c.clock()
	fp := hashFingerprint([]byte(notify.FingerprintKey(ev)))
	n := &storage.Notification{
		ID:             ulid.Make().String(),
		Fingerprint:    strconv.FormatUint(fp, 16),
		Kind:           string(ev.Kind),
		Severity:       string(ev.Severity),
		TaskName:       ev.TaskName,
		RunID:          runID(ev),
		Title:          title,
		Body:           body,
		Count:          1,
		Occurrences:    []time.Time{now},
		CreatedAt:      now,
		LastOccurredAt: now,
	}
	created, err := c.repo.UpsertByFingerprint(ctx, n, c.window(ev, now), c.cfg.CoalesceLimit)
	if err != nil {
		c.log.Error("notify coalescer: upsert failed", "fingerprint", n.Fingerprint, "error", err)
		return
	}
	updateType := UpdateTypeCreated
	if !created {
		updateType = UpdateTypeUpdated
	}
	count, err := c.repo.CountUnreadNotifications(ctx)
	if err != nil {
		// SSE clients can re-derive on next event or by polling; don't drop
		// the row update because the count query failed.
		c.log.Error("notify coalescer: unread count query failed", "error", err)
		count = -1
	}
	c.hub.Publish(Update{Type: updateType, Notification: *n, UnreadCount: count})
}

func hashFingerprint(b []byte) uint64 {
	h := fnv.New64a()
	_, _ = h.Write(b)
	return h.Sum64()
}

func runID(ev *notify.Event) string {
	if ev.Run == nil {
		return ""
	}
	return ev.Run.ID
}
