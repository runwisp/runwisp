// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"log/slog"
	"sync"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
)

// detectedURL is the Web UI base URL learned from the most recent signed-in
// visit. It stands in for [daemon] external_url when the config leaves that
// unset, so notification links work without any setup. It is persisted so the
// links survive a restart before anyone opens the Web UI again.
type detectedURL struct {
	db *storage.SQLiteDatabase

	mu  sync.Mutex
	url string
}

// loadDetectedURL seeds the holder from the value a previous boot persisted.
// A read failure only costs the links until the next visit, so it is logged
// rather than failing boot.
func loadDetectedURL(ctx context.Context, db *storage.SQLiteDatabase) *detectedURL {
	d := &detectedURL{db: db}
	u, _, err := db.GetConfigValue(ctx, storage.ConfigKeyDetectedExternalURL)
	if err != nil {
		slog.Warn("Failed to load the detected Web UI address", "err", err)
	}
	d.url = u
	return d
}

// Load returns the detected URL, or "" when no signed-in visit has been seen.
// A nil receiver reports "" so callers without detection need no guard.
func (d *detectedURL) Load() string {
	if d == nil {
		return ""
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.url
}

// Observe records the base URL of a signed-in request. It runs on every
// authenticated request, so an unchanged URL returns before touching storage.
func (d *detectedURL) Observe(u string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if u == d.url {
		return
	}
	d.url = u
	slog.Info("Detected the Web UI address from a signed-in visit", "url", u)
	if err := d.db.SetConfigValue(context.Background(), storage.ConfigKeyDetectedExternalURL, u); err != nil {
		slog.Warn("Failed to persist the detected Web UI address", "err", err)
	}
}

// externalURLFor resolves the base URL for notification links: the configured
// external_url wins, else the detected one.
func externalURLFor(cfg *config.Config, detected *detectedURL) func() string {
	return func() string {
		if u := cfg.Daemon.ExternalURL; u != "" {
			return u
		}
		return detected.Load()
	}
}
