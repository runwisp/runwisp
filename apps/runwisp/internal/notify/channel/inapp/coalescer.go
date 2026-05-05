// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package inapp

import (
	"container/list"
	"hash/fnv"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/storage"
)

// CoalescerConfig parameterizes the dedupe behavior.
type CoalescerConfig struct {
	Window      time.Duration // matches existing rows whose last_occurred_at falls within this span
	MaxIndex    int           // max in-memory fingerprints tracked (LRU)
	OccurrenceN int           // ring size of recorded occurrence timestamps
}

// Default values for CoalescerConfig.
const (
	DefaultWindow     = time.Hour
	DefaultMaxIndex   = 4096
	DefaultOccurrence = 10
)

// Coalescer folds repeats into a single persistent row, capping memory via
// LRU eviction. It owns its in-memory index; SQLite is authoritative — the
// index is a fast path, and any row not in the index is treated as a fresh
// occurrence (the storage UpsertByFingerprint then handles the within-window
// dedupe at the DB level).
type Coalescer struct {
	repo  storage.NotificationRepository
	hub   *Hub
	clock notify.Clock
	cfg   CoalescerConfig
	log   *slog.Logger

	mu    sync.Mutex
	index map[uint64]*list.Element
	order *list.List
}

type indexEntry struct {
	fingerprint    uint64
	id             string
	count          int
	occurrences    []time.Time
	lastOccurredAt time.Time
}

// NewCoalescer constructs a coalescer. Pass DefaultCoalescerConfig() for
// production defaults.
func NewCoalescer(repo storage.NotificationRepository, hub *Hub, clock notify.Clock, cfg CoalescerConfig, log *slog.Logger) *Coalescer {
	if cfg.Window == 0 {
		cfg.Window = DefaultWindow
	}
	if cfg.MaxIndex == 0 {
		cfg.MaxIndex = DefaultMaxIndex
	}
	if cfg.OccurrenceN == 0 {
		cfg.OccurrenceN = DefaultOccurrence
	}
	if log == nil {
		log = slog.Default()
	}
	if clock == nil {
		clock = notify.RealClock()
	}
	return &Coalescer{
		repo:  repo,
		hub:   hub,
		clock: clock,
		cfg:   cfg,
		log:   log,
		index: make(map[uint64]*list.Element),
		order: list.New(),
	}
}

// Receive folds the (rendered, ev) pair into the persistent store and
// publishes the resulting Update on the hub.
func (c *Coalescer) Receive(title, body string, ev *notify.Event) {
	if ev == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.clock.Now()
	fpRaw := fingerprintBytes(ev)
	fp := hashFingerprint(fpRaw)

	if elem, ok := c.index[fp]; ok {
		entry := elem.Value.(*indexEntry)
		if now.Sub(entry.lastOccurredAt) < c.cfg.Window {
			entry.count++
			entry.lastOccurredAt = now
			entry.occurrences = pushFront(entry.occurrences, now, c.cfg.OccurrenceN)
			c.order.MoveToFront(elem)

			if err := c.repo.UpdateOccurrence(entry.id, entry.count, now, entry.occurrences, title, body); err != nil {
				c.log.Error("notify coalescer: update failed", "id", entry.id, "error", err)
				return
			}
			n, err := c.repo.GetNotificationByID(entry.id)
			if err != nil {
				c.log.Error("notify coalescer: refetch after update failed", "id", entry.id, "error", err)
				return
			}
			c.hub.Publish(Update{Type: "notification.updated", Notification: *n})
			return
		}
		// Window elapsed; treat as new and let the storage layer dedupe at the
		// DB level (it will insert because the existing row is outside the
		// window).
	}

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
	created, err := c.repo.UpsertByFingerprint(n, c.cfg.Window, c.cfg.OccurrenceN)
	if err != nil {
		c.log.Error("notify coalescer: upsert failed", "fingerprint", n.Fingerprint, "error", err)
		return
	}
	updateType := "notification.created"
	if !created {
		updateType = "notification.updated"
	}

	entry := &indexEntry{
		fingerprint:    fp,
		id:             n.ID,
		count:          n.Count,
		occurrences:    n.Occurrences,
		lastOccurredAt: n.LastOccurredAt,
	}
	if existing, ok := c.index[fp]; ok {
		c.order.Remove(existing)
	}
	elem := c.order.PushFront(entry)
	c.index[fp] = elem
	c.evictIfFull()

	c.hub.Publish(Update{Type: updateType, Notification: *n})
}

// IndexSize returns the in-memory index size; diagnostic-only.
func (c *Coalescer) IndexSize() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

func (c *Coalescer) evictIfFull() {
	for c.order.Len() > c.cfg.MaxIndex {
		oldest := c.order.Back()
		if oldest == nil {
			return
		}
		entry := oldest.Value.(*indexEntry)
		c.order.Remove(oldest)
		delete(c.index, entry.fingerprint)
	}
}

func pushFront(slice []time.Time, t time.Time, max int) []time.Time {
	if max <= 0 {
		return append([]time.Time{t}, slice...)
	}
	out := make([]time.Time, 0, max)
	out = append(out, t)
	if len(slice) > 0 {
		if len(slice) > max-1 {
			slice = slice[:max-1]
		}
		out = append(out, slice...)
	}
	return out
}

func fingerprintBytes(ev *notify.Event) []byte {
	extraKey := ""
	if ev.Run != nil && ev.Run.EndReason != nil {
		extraKey = string(*ev.Run.EndReason)
	}
	if ev.Kind == notify.KindNotifyDeliveryFailed && ev.Extra != nil {
		ch, _ := ev.Extra["channel"].(string)
		ok, _ := ev.Extra["original_kind"].(string)
		extraKey = ch + "|" + ok
	}
	return []byte(string(ev.Kind) + "|" + ev.TaskName + "|" + extraKey)
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
