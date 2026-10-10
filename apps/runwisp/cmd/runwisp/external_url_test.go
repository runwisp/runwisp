// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
)

// TestDetectedURL_LastVisitWinsAndSurvivesRestart: each new signed-in address
// replaces the previous one, and a fresh boot picks up the last one.
func TestDetectedURL_LastVisitWinsAndSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	db, err := storage.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	d := loadDetectedURL(ctx, db)
	assert.Empty(t, d.Load())

	d.Observe("http://192.168.1.50:9477")
	d.Observe("https://runwisp.example.com")
	assert.Equal(t, "https://runwisp.example.com", d.Load())

	assert.Equal(t, "https://runwisp.example.com", loadDetectedURL(ctx, db).Load())
}

// TestDetectedURL_LoadDoesNotWaitForPersist: renderers and /api/daemon read
// the URL while a sign-in may be writing it to SQLite; the read must not queue
// behind that write.
func TestDetectedURL_LoadDoesNotWaitForPersist(t *testing.T) {
	d := &detectedURL{}
	d.url.Store("http://rw.lan:9477")
	d.writeMu.Lock() // a sign-in mid-persist
	defer d.writeMu.Unlock()

	done := make(chan string)
	go func() { done <- d.Load() }()
	select {
	case got := <-done:
		assert.Equal(t, "http://rw.lan:9477", got)
	case <-time.After(5 * time.Second):
		t.Fatal("Load blocked behind an in-flight Observe")
	}
}

// TestExternalURLFor_ConfigWins: a configured external_url is never replaced
// by a detected address; detection only fills in when it is unset.
func TestExternalURLFor_ConfigWins(t *testing.T) {
	db, err := storage.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	d := loadDetectedURL(context.Background(), db)
	d.Observe("http://rw.lan:9477")

	configured := &config.Config{Daemon: config.Daemon{ExternalURL: "https://pinned.example.com"}}
	assert.Equal(t, "https://pinned.example.com", externalURLFor(configured, d)())
	assert.Equal(t, "http://rw.lan:9477", externalURLFor(&config.Config{}, d)())
	assert.Empty(t, externalURLFor(&config.Config{}, nil)())
}
