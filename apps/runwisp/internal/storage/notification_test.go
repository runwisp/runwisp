// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupNotificationDB(t *testing.T) Database {
	t.Helper()
	db, err := New(":memory:", nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newNotification(now time.Time, fp string) *Notification {
	return &Notification{
		ID:             ulid.Make().String(),
		Fingerprint:    fp,
		Kind:           "run.failed",
		Severity:       "error",
		TaskName:       "backup-db",
		Title:          "backup-db failed",
		Body:           "Exit 1 after 4m",
		Count:          1,
		Occurrences:    []time.Time{now},
		CreatedAt:      now,
		LastOccurredAt: now,
	}
}

func TestUpsertByFingerprint_InsertsFreshRow(t *testing.T) {
	db := setupNotificationDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	n := newNotification(now, "fp-1")
	created, err := db.UpsertByFingerprint(n, time.Hour, 10)
	require.NoError(t, err)
	assert.True(t, created, "first call must insert")

	got, err := db.GetNotificationByID(n.ID)
	require.NoError(t, err)
	assert.Equal(t, "fp-1", got.Fingerprint)
	assert.Equal(t, 1, got.Count)
	assert.Len(t, got.Occurrences, 1)
}

func TestUpsertByFingerprint_CoalescesWithinWindow(t *testing.T) {
	db := setupNotificationDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	first := newNotification(now, "fp-2")
	created, err := db.UpsertByFingerprint(first, time.Hour, 10)
	require.NoError(t, err)
	require.True(t, created)

	second := newNotification(now.Add(5*time.Minute), "fp-2")
	second.Occurrences = []time.Time{second.LastOccurredAt} // simulated ring cap of 10
	// Set ring cap by passing a slice with len=10 limit semantics: caller sets cap.
	// Here we mimic Coalescer: cap by occurrence_ring at runtime via len(slice)+truncate.
	// We rely on the repo's truncate-by-len behavior.
	created, err = db.UpsertByFingerprint(second, time.Hour, 10)
	require.NoError(t, err)
	assert.False(t, created, "second call within window must update")

	got, err := db.GetNotificationByID(first.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, got.Count)
	assert.Equal(t, second.LastOccurredAt.Unix(), got.LastOccurredAt.Unix())
	assert.Len(t, got.Occurrences, 2)
	// Newest first.
	assert.Equal(t, second.LastOccurredAt.Unix(), got.Occurrences[0].Unix())
}

func TestUpsertByFingerprint_InsertsAfterWindowExpires(t *testing.T) {
	db := setupNotificationDB(t)
	old := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)

	first := newNotification(old, "fp-3")
	_, err := db.UpsertByFingerprint(first, time.Hour, 10)
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Second)
	second := newNotification(now, "fp-3")
	created, err := db.UpsertByFingerprint(second, time.Hour, 10)
	require.NoError(t, err)
	assert.True(t, created, "outside window must insert")

	rows, err := db.ListNotifications(10, "")
	require.NoError(t, err)
	assert.Len(t, rows, 2)
}

func TestUpdateOccurrence(t *testing.T) {
	db := setupNotificationDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	n := newNotification(now, "fp-4")
	_, err := db.UpsertByFingerprint(n, time.Hour, 10)
	require.NoError(t, err)

	later := now.Add(time.Minute)
	occ := []time.Time{later, now}
	require.NoError(t, db.UpdateOccurrence(n.ID, 2, later, occ, "new-title", "new-body"))

	got, err := db.GetNotificationByID(n.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, got.Count)
	assert.Equal(t, later.Unix(), got.LastOccurredAt.Unix())
	assert.Equal(t, "new-title", got.Title)
	assert.Equal(t, "new-body", got.Body)
	assert.Len(t, got.Occurrences, 2)
}

func TestUpdateOccurrence_NotFound(t *testing.T) {
	db := setupNotificationDB(t)
	err := db.UpdateOccurrence("missing", 1, time.Now(), nil, "", "")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestListNotifications_PaginationByULIDCursor(t *testing.T) {
	db := setupNotificationDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	const total = 5
	ids := make([]string, total)
	for i := 0; i < total; i++ {
		n := newNotification(now.Add(time.Duration(i)*time.Millisecond), "fp-list")
		// Each call has the same fingerprint within the window: only the first
		// inserts; subsequent ones update the same row. To get distinct rows,
		// vary fingerprint per iteration.
		n.Fingerprint = "fp-" + ulid.Make().String()
		_, err := db.UpsertByFingerprint(n, time.Hour, 10)
		require.NoError(t, err)
		ids[i] = n.ID
	}

	page1, err := db.ListNotifications(2, "")
	require.NoError(t, err)
	require.Len(t, page1, 2)

	page2, err := db.ListNotifications(2, page1[len(page1)-1].ID)
	require.NoError(t, err)
	require.Len(t, page2, 2)
	assert.NotEqual(t, page1[0].ID, page2[0].ID)
}

func TestPruneByCount(t *testing.T) {
	db := setupNotificationDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	for i := 0; i < 10; i++ {
		n := newNotification(now.Add(time.Duration(i)*time.Millisecond), "")
		n.Fingerprint = "fp-" + ulid.Make().String()
		_, err := db.UpsertByFingerprint(n, time.Hour, 10)
		require.NoError(t, err)
	}

	deleted, err := db.PruneNotificationsByCount(3)
	require.NoError(t, err)
	assert.EqualValues(t, 7, deleted)

	rows, err := db.ListNotifications(50, "")
	require.NoError(t, err)
	assert.Len(t, rows, 3)
}

func TestPruneByAge(t *testing.T) {
	db := setupNotificationDB(t)

	old := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	n1 := newNotification(old, "fp-old")
	_, err := db.UpsertByFingerprint(n1, time.Hour, 10)
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Second)
	n2 := newNotification(now, "fp-new")
	_, err = db.UpsertByFingerprint(n2, time.Hour, 10)
	require.NoError(t, err)

	deleted, err := db.PruneNotificationsByAge(24 * time.Hour)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)

	rows, err := db.ListNotifications(50, "")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "fp-new", rows[0].Fingerprint)
}

func TestReadState_RoundTrip(t *testing.T) {
	db := setupNotificationDB(t)

	got, err := db.GetLastReadAt()
	require.NoError(t, err)
	assert.True(t, got.IsZero(), "unset read-state must return zero time")

	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, db.SetLastReadAt(now))

	got, err = db.GetLastReadAt()
	require.NoError(t, err)
	assert.Equal(t, now.Unix(), got.Unix())

	// Update path.
	later := now.Add(time.Hour)
	require.NoError(t, db.SetLastReadAt(later))
	got, err = db.GetLastReadAt()
	require.NoError(t, err)
	assert.Equal(t, later.Unix(), got.Unix())
}

func TestCountNotificationsSince(t *testing.T) {
	db := setupNotificationDB(t)
	mark := time.Now().UTC().Truncate(time.Second)

	older := newNotification(mark.Add(-time.Hour), "fp-a")
	_, err := db.UpsertByFingerprint(older, time.Minute, 10)
	require.NoError(t, err)

	newer := newNotification(mark.Add(time.Hour), "fp-b")
	_, err = db.UpsertByFingerprint(newer, time.Minute, 10)
	require.NoError(t, err)

	c, err := db.CountNotificationsSince(mark)
	require.NoError(t, err)
	assert.EqualValues(t, 1, c)
}
