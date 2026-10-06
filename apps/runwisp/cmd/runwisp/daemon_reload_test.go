// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/stretchr/testify/require"
)

// TestSettingsApplier_RebuildsNotifyOnTemplateEdit proves a reload notices an
// edited template_path file even though runwisp.toml is unchanged: prepare
// rebuilds the notification service, so a broken template rejects the reload
// instead of being skipped until the next restart.
func TestSettingsApplier_RebuildsNotifyOnTemplateEdit(t *testing.T) {
	db, err := storage.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	tmpl := filepath.Join(t.TempDir(), "slack.tmpl")
	require.NoError(t, os.WriteFile(tmpl, []byte("{{ .TaskName }} failed"), 0o600))
	cfg := &config.Config{Notify: config.NotifyConfig{Notifiers: []config.NotifierSpec{
		{ID: "ops", Type: "slack", WebhookURL: "https://hooks.example/abc", TemplatePath: tmpl},
	}}}

	a := &settingsApplier{
		svc:       &daemonServices{DB: db, EventBus: events.NewEventBus(), Notify: newLiveNotify()},
		templates: notifyTemplates(cfg.Notify),
	}
	_, err = a.prepare(cfg, cfg)
	require.NoError(t, err, "nothing changed, nothing to rebuild")

	require.NoError(t, os.WriteFile(tmpl, []byte("{{ .TaskName "), 0o600))
	_, err = a.prepare(cfg, cfg)
	require.ErrorContains(t, err, "notifications")
}
