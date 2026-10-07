// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"io/fs"
	"reflect"
	"testing"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeFiles serves template_path reads from memory; a missing path reads as
// fs.ErrNotExist.
type fakeFiles map[string]string

func (f fakeFiles) read(path string) ([]byte, error) {
	body, ok := f[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return []byte(body), nil
}

// newTestApplier returns an applier whose live notify state was built from
// cfg's templates as files reads them, the way boot leaves it.
func newTestApplier(t *testing.T, cfg *config.Config, files fakeFiles) *settingsApplier {
	t.Helper()
	db, err := storage.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	live := newLiveNotify()
	live.templates, _ = readNotifyTemplates(cfg.Notify, files.read)
	return &settingsApplier{
		svc:      &daemonServices{DB: db, EventBus: events.NewEventBus(), Notify: live},
		readFile: files.read,
	}
}

// TestSettingsApplier_TemplateEdit proves a reload notices an edited
// template_path file even though runwisp.toml is unchanged: the edit is
// reported as "notifications", and a broken template rejects the reload
// instead of being skipped until the next restart.
func TestSettingsApplier_TemplateEdit(t *testing.T) {
	cfg := &config.Config{Notify: config.NotifyConfig{Notifiers: []config.NotifierSpec{
		{ID: "ops", Type: "slack", WebhookURL: "https://hooks.example/abc", TemplatePath: "/etc/runwisp/slack.tmpl"},
	}}}
	files := fakeFiles{"/etc/runwisp/slack.tmpl": `{"text": "{{ .TaskName }} failed"}`}
	a := newTestApplier(t, cfg, files)

	keys, _, err := a.prepare(cfg, cfg)
	require.NoError(t, err)
	assert.Empty(t, keys, "nothing changed, nothing to rebuild")

	files["/etc/runwisp/slack.tmpl"] = `{"text": "{{ .TaskName }} broke"}`
	keys, _, err = a.prepare(cfg, cfg)
	require.NoError(t, err)
	assert.Equal(t, []string{"notifications"}, keys)

	files["/etc/runwisp/slack.tmpl"] = "{{ .TaskName "
	_, _, err = a.prepare(cfg, cfg)
	require.ErrorContains(t, err, "notifications")
}

// TestSettingsApplier_UnreadableTemplate proves a template that can't be read
// rejects a reload that has to rebuild notifications, while one that was
// already unreadable at boot doesn't block an unrelated reload.
func TestSettingsApplier_UnreadableTemplate(t *testing.T) {
	cfg := &config.Config{Notify: config.NotifyConfig{Notifiers: []config.NotifierSpec{
		{ID: "ops", Type: "slack", WebhookURL: "https://hooks.example/abc", TemplatePath: "/missing.tmpl"},
	}}}
	a := newTestApplier(t, cfg, fakeFiles{})

	keys, _, err := a.prepare(cfg, cfg)
	require.NoError(t, err)
	assert.Empty(t, keys)

	updated := *cfg
	updated.Notify.CoalesceLimit = 5
	_, _, err = a.prepare(cfg, &updated)
	require.ErrorContains(t, err, "read template /missing.tmpl")
}

// TestReloadCoversEveryDaemonKey guards config.Daemon growing a field nobody
// classified: every field must either be rejected as restart-only or be
// reported, and so applied, by the settings applier. A field that is neither
// would be silently accepted by reload and never take effect.
func TestReloadCoversEveryDaemonKey(t *testing.T) {
	a := newTestApplier(t, &config.Config{}, fakeFiles{})
	typ := reflect.TypeFor[config.Daemon]()
	for i := range typ.NumField() {
		field := typ.Field(i)
		t.Run(field.Name, func(t *testing.T) {
			updated := &config.Config{}
			v := reflect.ValueOf(&updated.Daemon).Elem().Field(i)
			switch v.Kind() {
			case reflect.Bool:
				v.SetBool(true)
			case reflect.String:
				v.SetString("x")
			case reflect.Int64:
				v.SetInt(1)
			case reflect.Slice:
				v.Set(reflect.Append(v, reflect.ValueOf("x")))
			default:
				t.Fatalf("unhandled kind %s; extend this test", v.Kind())
			}
			rejected := runtime.CheckNonReloadable(&config.Config{}, updated) != nil
			keys, _, err := a.prepare(&config.Config{}, updated)
			require.NoError(t, err)
			reported := len(keys) > 0
			assert.True(t, rejected != reported,
				"[daemon] %s must be exactly one of restart-only or applied live (rejected=%v reported=%v)",
				field.Name, rejected, reported)
		})
	}
}

func TestSettingsApplier_ReportsChangedKeys(t *testing.T) {
	old := &config.Config{}
	a := newTestApplier(t, old, fakeFiles{})
	keys, _, err := a.prepare(old, old)
	require.NoError(t, err)
	assert.Empty(t, keys)

	updated := &config.Config{
		Storage: config.Storage{MaxSize: 1},
		Notify:  config.NotifyConfig{Routes: []config.NotificationRoute{{NotifierID: []string{"inapp"}}}},
	}
	keys, _, err = a.prepare(old, updated)
	require.NoError(t, err)
	assert.Equal(t, []string{"storage.max_size", "notifications"}, keys)
}
