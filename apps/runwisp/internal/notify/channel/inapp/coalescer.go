// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package inapp

import (
	"hash/fnv"
	"log/slog"
	"strconv"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/storage"
)

// CoalescerConfig parameterizes the dedupe behavior.
type CoalescerConfig struct {
	Window      time.Duration // matches existing rows whose last_occurred_at falls within this span
	OccurrenceN int           // ring size of recorded occurrence timestamps
}

// Default values for CoalescerConfig.
const (
	DefaultWindow     = time.Hour
	DefaultOccurrence = 10
)

// Coalescer folds repeats into a single persistent row. SQLite is the
// authoritative store; UpsertByFingerprint owns dedupe in a single transaction
// (SELECT-or-INSERT-or-UPDATE), so the Coalescer is a thin glue layer.
type Coalescer struct {
	repo  storage.NotificationRepository
	hub   *Hub
	clock notify.Clock
	cfg   CoalescerConfig
	log   *slog.Logger
}

// NewCoalescer constructs a coalescer.
func NewCoalescer(repo storage.NotificationRepository, hub *Hub, clock notify.Clock, cfg CoalescerConfig, log *slog.Logger) *Coalescer {
	if cfg.Window == 0 {
		cfg.Window = DefaultWindow
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
	}
}

// Receive folds the (rendered, ev) pair into the persistent store and
// publishes the resulting Update on the hub.
func (c *Coalescer) Receive(title, body string, ev *notify.Event) {
	if ev == nil {
		return
	}
	now := c.clock.Now()
	fp := hashFingerprint(fingerprintBytes(ev))
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
	c.hub.Publish(Update{Type: updateType, Notification: *n})
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
