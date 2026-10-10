// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"testing"

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
