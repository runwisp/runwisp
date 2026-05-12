// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package inapp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/testutil"
	"github.com/runwisp/runwisp/internal/storage"
)

func newDB(t *testing.T) storage.Database {
	t.Helper()
	db, err := storage.New(":memory:", nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func makeEvent(task string) *notify.Event {
	return &notify.Event{
		Kind:     notify.KindRunFailed,
		Severity: notify.SevError,
		TaskName: task,
	}
}

func TestCoalescer_FoldsRepeatsWithinWindow(t *testing.T) {
	db := newDB(t)
	hub := NewHub(8, 50)
	clk := testutil.NewFakeClock(time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC))
	c := NewCoalescer(db, hub, clk, CoalescerConfig{Window: time.Hour, OccurrenceN: 5}, nil)

	c.Receive("title", "first body", makeEvent("backup-db"))
	clk.Advance(5 * time.Minute)
	c.Receive("title", "second body", makeEvent("backup-db"))
	clk.Advance(10 * time.Minute)
	c.Receive("title", "third body", makeEvent("backup-db"))

	rows, err := db.ListNotifications(50, "")
	require.NoError(t, err)
	require.Len(t, rows, 1, "all three within window must coalesce")
	assert.Equal(t, 3, rows[0].Count)
	assert.Len(t, rows[0].Occurrences, 3)
}

func TestCoalescer_InsertsNewRowAfterWindow(t *testing.T) {
	db := newDB(t)
	hub := NewHub(8, 50)
	clk := testutil.NewFakeClock(time.Date(2026, 5, 4, 12, 0, 0, 0, time.UTC))
	c := NewCoalescer(db, hub, clk, CoalescerConfig{Window: time.Hour, OccurrenceN: 5}, nil)

	c.Receive("t", "b", makeEvent("backup-db"))
	clk.Advance(2 * time.Hour)
	c.Receive("t", "b", makeEvent("backup-db"))

	rows, err := db.ListNotifications(50, "")
	require.NoError(t, err)
	require.Len(t, rows, 2, "outside window must produce a new row")
}

func TestCoalescer_SeparatesByFingerprint(t *testing.T) {
	db := newDB(t)
	hub := NewHub(8, 50)
	clk := testutil.NewFakeClock(time.Now().UTC())
	c := NewCoalescer(db, hub, clk, CoalescerConfig{Window: time.Hour, OccurrenceN: 5}, nil)

	c.Receive("t", "b", makeEvent("alpha"))
	c.Receive("t", "b", makeEvent("beta"))
	c.Receive("t", "b", makeEvent("alpha"))

	rows, err := db.ListNotifications(50, "")
	require.NoError(t, err)
	assert.Len(t, rows, 2, "different task names → different fingerprints")
}

func TestCoalescer_OccurrenceRingTrimmed(t *testing.T) {
	db := newDB(t)
	hub := NewHub(8, 50)
	clk := testutil.NewFakeClock(time.Now().UTC())
	const ringSize = 4
	c := NewCoalescer(db, hub, clk, CoalescerConfig{Window: time.Hour, OccurrenceN: ringSize}, nil)

	for i := 0; i < 20; i++ {
		c.Receive("t", "b", makeEvent("backup-db"))
		clk.Advance(time.Second)
	}
	rows, err := db.ListNotifications(50, "")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, 20, rows[0].Count)
	assert.LessOrEqual(t, len(rows[0].Occurrences), ringSize)
}

func TestCoalescer_PublishesCreatedThenUpdated(t *testing.T) {
	db := newDB(t)
	hub := NewHub(16, 50)
	sub, unsub := hub.Subscribe()
	defer unsub()

	clk := testutil.NewFakeClock(time.Now().UTC())
	c := NewCoalescer(db, hub, clk, CoalescerConfig{Window: time.Hour, OccurrenceN: 5}, nil)

	c.Receive("t", "b", makeEvent("backup-db"))
	clk.Advance(time.Minute)
	c.Receive("t", "b", makeEvent("backup-db"))

	deadline := time.After(time.Second)
	var got []Update
loop:
	for len(got) < 2 {
		select {
		case u := <-sub.Channel():
			got = append(got, u)
		case <-deadline:
			break loop
		}
	}
	require.Len(t, got, 2)
	assert.Equal(t, "notification.created", got[0].Type)
	assert.Equal(t, "notification.updated", got[1].Type)
	assert.Equal(t, got[0].Notification.ID, got[1].Notification.ID, "same row")
	// The coalescer queries CountUnreadNotifications post-upsert and ships it
	// on every update so SSE clients can replace their badge instead of
	// delta-tracking. Both events here come from a single still-unread row.
	assert.EqualValues(t, 1, got[0].UnreadCount)
	assert.EqualValues(t, 1, got[1].UnreadCount)
}

func TestCoalescer_DistinctFingerprintsAllPersist(t *testing.T) {
	db := newDB(t)
	hub := NewHub(8, 50)
	clk := testutil.NewFakeClock(time.Now().UTC())
	c := NewCoalescer(db, hub, clk, CoalescerConfig{Window: time.Hour, OccurrenceN: 5}, nil)

	for _, name := range []string{"a", "b", "c", "d", "e"} {
		c.Receive("t", "b", makeEvent(name))
		clk.Advance(time.Second)
	}
	rows, err := db.ListNotifications(50, "")
	require.NoError(t, err)
	assert.Len(t, rows, 5, "each distinct fingerprint persists as its own row")
}
