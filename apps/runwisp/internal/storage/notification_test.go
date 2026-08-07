// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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
	db, err := New(":memory:")
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
	ctx := t.Context()
	db := setupNotificationDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	n := newNotification(now, "fp-1")
	created, err := db.UpsertByFingerprint(ctx, n, time.Hour, 10)
	require.NoError(t, err)
	assert.True(t, created, "first call must insert")

	got, err := db.GetNotificationByID(ctx, n.ID)
	require.NoError(t, err)
	assert.Equal(t, "fp-1", got.Fingerprint)
	assert.Equal(t, 1, got.Count)
	assert.Len(t, got.Occurrences, 1)
}

func TestUpsertByFingerprint_CoalescesWithinWindow(t *testing.T) {
	ctx := t.Context()
	db := setupNotificationDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	first := newNotification(now, "fp-2")
	created, err := db.UpsertByFingerprint(ctx, first, time.Hour, 10)
	require.NoError(t, err)
	require.True(t, created)

	second := newNotification(now.Add(5*time.Minute), "fp-2")
	second.Occurrences = []time.Time{second.LastOccurredAt} // simulated ring cap of 10
	created, err = db.UpsertByFingerprint(ctx, second, time.Hour, 10)
	require.NoError(t, err)
	assert.False(t, created, "second call within window must update")

	got, err := db.GetNotificationByID(ctx, first.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, got.Count)
	assert.Equal(t, second.LastOccurredAt.Unix(), got.LastOccurredAt.Unix())
	assert.Len(t, got.Occurrences, 2)
	// Newest first.
	assert.Equal(t, second.LastOccurredAt.Unix(), got.Occurrences[0].Unix())
}

// On a coalesced update the passed-in notification pointer must be rewritten to
// carry the FIRST occurrence's created_at and run_id — not the current event's —
// so callers (e.g. the in-app coalescer's SSE payload) match what a later read
// of the persisted row returns.
func TestUpsertByFingerprint_CoalesceKeepsFirstSeenMetadata(t *testing.T) {
	ctx := t.Context()
	db := setupNotificationDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	first := newNotification(now, "fp-meta")
	first.RunID = "run-first"
	_, err := db.UpsertByFingerprint(ctx, first, time.Hour, 10)
	require.NoError(t, err)

	second := newNotification(now.Add(30*time.Minute), "fp-meta")
	second.RunID = "run-second"
	created, err := db.UpsertByFingerprint(ctx, second, time.Hour, 10)
	require.NoError(t, err)
	require.False(t, created)

	assert.Equal(t, first.CreatedAt.Unix(), second.CreatedAt.Unix(), "created_at must stay first-seen")
	assert.Equal(t, "run-first", second.RunID, "run_id must stay first-seen")
}

func TestUpsertByFingerprint_InsertsAfterWindowExpires(t *testing.T) {
	ctx := t.Context()
	db := setupNotificationDB(t)
	old := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)

	first := newNotification(old, "fp-3")
	_, err := db.UpsertByFingerprint(ctx, first, time.Hour, 10)
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Second)
	second := newNotification(now, "fp-3")
	created, err := db.UpsertByFingerprint(ctx, second, time.Hour, 10)
	require.NoError(t, err)
	assert.True(t, created, "outside window must insert")

	rows, err := db.ListNotifications(ctx, 10, "")
	require.NoError(t, err)
	assert.Len(t, rows, 2)
}

func TestListNotifications_PaginationByULIDCursor(t *testing.T) {
	ctx := t.Context()
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
		_, err := db.UpsertByFingerprint(ctx, n, time.Hour, 10)
		require.NoError(t, err)
		ids[i] = n.ID
	}

	page1, err := db.ListNotifications(ctx, 2, "")
	require.NoError(t, err)
	require.Len(t, page1, 2)

	page2, err := db.ListNotifications(ctx, 2, page1[len(page1)-1].ID)
	require.NoError(t, err)
	require.Len(t, page2, 2)
	assert.NotEqual(t, page1[0].ID, page2[0].ID)
}

func TestPruneByCount(t *testing.T) {
	ctx := t.Context()
	db := setupNotificationDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	for i := 0; i < 10; i++ {
		n := newNotification(now.Add(time.Duration(i)*time.Millisecond), "")
		n.Fingerprint = "fp-" + ulid.Make().String()
		_, err := db.UpsertByFingerprint(ctx, n, time.Hour, 10)
		require.NoError(t, err)
	}

	deleted, err := db.PruneNotificationsByCount(ctx, 3)
	require.NoError(t, err)
	assert.EqualValues(t, 7, deleted)

	rows, err := db.ListNotifications(ctx, 50, "")
	require.NoError(t, err)
	assert.Len(t, rows, 3)
}

func TestPruneByCount_KeepLEZeroIsNoop(t *testing.T) {
	ctx := t.Context()
	db := setupNotificationDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	_, err := db.UpsertByFingerprint(ctx, newNotification(now, "fp-1"), time.Hour, 10)
	require.NoError(t, err)

	deleted, err := db.PruneNotificationsByCount(ctx, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 0, deleted, "keep<=0 must be a no-op")

	deleted, err = db.PruneNotificationsByCount(ctx, -5)
	require.NoError(t, err)
	assert.EqualValues(t, 0, deleted, "negative keep must be a no-op")
}

func TestPruneByAge_OlderThanLEZeroIsNoop(t *testing.T) {
	ctx := t.Context()
	db := setupNotificationDB(t)
	now := time.Now().UTC().Truncate(time.Second)
	_, err := db.UpsertByFingerprint(ctx, newNotification(now, "fp-1"), time.Hour, 10)
	require.NoError(t, err)

	deleted, err := db.PruneNotificationsByAge(ctx, 0)
	require.NoError(t, err)
	assert.EqualValues(t, 0, deleted, "olderThan<=0 must be a no-op")
}

func TestGetNotificationByID_NotFoundReturnsErrNotFound(t *testing.T) {
	ctx := t.Context()
	db := setupNotificationDB(t)
	_, err := db.GetNotificationByID(ctx, ulid.Make().String())
	require.ErrorIs(t, err, ErrNotFound)
}

func TestPruneByAge(t *testing.T) {
	ctx := t.Context()
	db := setupNotificationDB(t)

	old := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	n1 := newNotification(old, "fp-old")
	_, err := db.UpsertByFingerprint(ctx, n1, time.Hour, 10)
	require.NoError(t, err)

	now := time.Now().UTC().Truncate(time.Second)
	n2 := newNotification(now, "fp-new")
	_, err = db.UpsertByFingerprint(ctx, n2, time.Hour, 10)
	require.NoError(t, err)

	deleted, err := db.PruneNotificationsByAge(ctx, 24*time.Hour)
	require.NoError(t, err)
	assert.EqualValues(t, 1, deleted)

	rows, err := db.ListNotifications(ctx, 50, "")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "fp-new", rows[0].Fingerprint)
}

func TestMarkNotificationRead_Stamps(t *testing.T) {
	ctx := t.Context()
	db := setupNotificationDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	n := newNotification(now, "fp-read-1")
	_, err := db.UpsertByFingerprint(ctx, n, time.Hour, 10)
	require.NoError(t, err)

	stamp := now.Add(time.Minute)
	got, err := db.MarkNotificationRead(ctx, n.ID, stamp)
	require.NoError(t, err)
	require.NotNil(t, got.ReadAt)
	assert.Equal(t, stamp.Unix(), got.ReadAt.Unix())
}

func TestMarkNotificationRead_NotFound(t *testing.T) {
	ctx := t.Context()
	db := setupNotificationDB(t)
	_, err := db.MarkNotificationRead(ctx, "does-not-exist", time.Now())
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestMarkNotificationUnread_ClearsReadAt(t *testing.T) {
	ctx := t.Context()
	db := setupNotificationDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	n := newNotification(now, "fp-unread")
	_, err := db.UpsertByFingerprint(ctx, n, time.Hour, 10)
	require.NoError(t, err)
	_, err = db.MarkNotificationRead(ctx, n.ID, now.Add(time.Minute))
	require.NoError(t, err)

	got, err := db.MarkNotificationUnread(ctx, n.ID)
	require.NoError(t, err)
	assert.Nil(t, got.ReadAt)
}

func TestMarkAllNotificationsRead_StampsOnlyUnread(t *testing.T) {
	ctx := t.Context()
	db := setupNotificationDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	a := newNotification(now, "fp-a")
	_, err := db.UpsertByFingerprint(ctx, a, time.Hour, 10)
	require.NoError(t, err)
	b := newNotification(now, "fp-b")
	_, err = db.UpsertByFingerprint(ctx, b, time.Hour, 10)
	require.NoError(t, err)

	earlier := now.Add(-time.Hour)
	_, err = db.MarkNotificationRead(ctx, a.ID, earlier)
	require.NoError(t, err)

	stamp := now.Add(time.Minute)
	require.NoError(t, db.MarkAllNotificationsRead(ctx, stamp))

	gotA, err := db.GetNotificationByID(ctx, a.ID)
	require.NoError(t, err)
	require.NotNil(t, gotA.ReadAt)
	assert.Equal(t, earlier.Unix(), gotA.ReadAt.Unix(), "already-read row must keep its stamp")

	gotB, err := db.GetNotificationByID(ctx, b.ID)
	require.NoError(t, err)
	require.NotNil(t, gotB.ReadAt)
	assert.Equal(t, stamp.Unix(), gotB.ReadAt.Unix())
}

func TestCountUnreadNotifications(t *testing.T) {
	ctx := t.Context()
	db := setupNotificationDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	a := newNotification(now, "fp-a")
	_, err := db.UpsertByFingerprint(ctx, a, time.Hour, 10)
	require.NoError(t, err)
	b := newNotification(now, "fp-b")
	_, err = db.UpsertByFingerprint(ctx, b, time.Hour, 10)
	require.NoError(t, err)

	c, err := db.CountUnreadNotifications(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 2, c)

	_, err = db.MarkNotificationRead(ctx, a.ID, now.Add(time.Minute))
	require.NoError(t, err)

	c, err = db.CountUnreadNotifications(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 1, c)
}

// --- encodeOccurrences / decodeOccurrences ---

func TestEncodeOccurrences_Nil(t *testing.T) {
	// nil input should marshal to empty array, not "null"
	s, err := encodeOccurrences(nil)
	require.NoError(t, err)
	assert.Equal(t, "[]", s)
}

func TestEncodeOccurrences_NonNil(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s, err := encodeOccurrences([]time.Time{now})
	require.NoError(t, err)
	assert.Contains(t, s, `"`)
}

func TestDecodeOccurrences_Empty(t *testing.T) {
	ts, err := decodeOccurrences("")
	require.NoError(t, err)
	assert.Nil(t, ts)
}

func TestDecodeOccurrences_ValidJSON(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	encoded, err := encodeOccurrences([]time.Time{now})
	require.NoError(t, err)

	decoded, err := decodeOccurrences(encoded)
	require.NoError(t, err)
	require.Len(t, decoded, 1)
	assert.Equal(t, now, decoded[0].UTC().Truncate(time.Second))
}

func TestDecodeOccurrences_InvalidJSON(t *testing.T) {
	_, err := decodeOccurrences("not-json")
	require.Error(t, err)
}

func TestUpsertByFingerprint_CoalesceClearsReadAt(t *testing.T) {
	ctx := t.Context()
	db := setupNotificationDB(t)
	now := time.Now().UTC().Truncate(time.Second)

	first := newNotification(now, "fp-coalesce")
	_, err := db.UpsertByFingerprint(ctx, first, time.Hour, 10)
	require.NoError(t, err)
	_, err = db.MarkNotificationRead(ctx, first.ID, now.Add(time.Minute))
	require.NoError(t, err)

	second := newNotification(now.Add(5*time.Minute), "fp-coalesce")
	_, err = db.UpsertByFingerprint(ctx, second, time.Hour, 10)
	require.NoError(t, err)

	got, err := db.GetNotificationByID(ctx, first.ID)
	require.NoError(t, err)
	assert.Nil(t, got.ReadAt, "fresh occurrence must reset read state")
}
