// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package inapp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/testutil"
)

func updateEvent(version string) *notify.Event {
	return &notify.Event{
		Kind:     notify.KindUpdateAvailable,
		Severity: notify.SevInfo,
		Extra:    map[string]any{"latest_version": version},
	}
}

func TestAnnouncer_AnnouncesOncePerFingerprint(t *testing.T) {
	db := newDB(t)
	hub := NewHub(16)
	sub, unsub := hub.Subscribe()
	defer unsub()

	clk := testutil.NewFakeClock(time.Now().UTC())
	a := NewAnnouncer(db, hub, clk)

	// First announce: persists + publishes a created event.
	require.NoError(t, a.Announce(context.Background(), updateEvent("0.17.0"), "RunWisp 0.17.0 is available", "body"))
	// Second announce of the same version: no new row, no new event.
	clk.Advance(6 * time.Hour)
	require.NoError(t, a.Announce(context.Background(), updateEvent("0.17.0"), "RunWisp 0.17.0 is available", "body"))

	rows, err := db.ListNotifications(context.Background(), 50, "")
	require.NoError(t, err)
	require.Len(t, rows, 1, "same version must not duplicate")
	assert.Equal(t, "update.available", rows[0].Kind)
	assert.Equal(t, "info", rows[0].Severity)

	var got []Update
	select {
	case u := <-sub.Channel():
		got = append(got, u)
	case <-time.After(time.Second):
		t.Fatal("expected a created event on first announce")
	}
	// No second event should be waiting.
	select {
	case u := <-sub.Channel():
		t.Fatalf("unexpected second publish: %+v", u)
	case <-time.After(50 * time.Millisecond):
	}
	require.Len(t, got, 1)
	assert.Equal(t, UpdateTypeCreated, got[0].Type)
	assert.EqualValues(t, 1, got[0].UnreadCount)
}

func TestAnnouncer_NilHubStillPersists(t *testing.T) {
	db := newDB(t)
	a := NewAnnouncer(db, nil, testutil.NewFakeClock(time.Now().UTC()))

	require.NoError(t, a.Announce(context.Background(), updateEvent("0.18.0"), "title", "body"))

	rows, err := db.ListNotifications(context.Background(), 50, "")
	require.NoError(t, err)
	require.Len(t, rows, 1, "row must persist even without a hub")
}
