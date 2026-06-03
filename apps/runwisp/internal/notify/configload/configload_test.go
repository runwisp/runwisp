// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package configload

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/render"
)

func TestResolve_InlineLiteralPassthrough(t *testing.T) {
	cfg := config.NotifyConfig{
		Notifiers: []config.NotifierSpec{{
			ID:         "ops",
			Type:       "slack",
			WebhookURL: "https://hooks.slack.test/T/B/Z",
		}},
	}
	got, err := Resolve(cfg, t.TempDir(), render.TemplateContext{})
	require.NoError(t, err)
	require.Len(t, got.Notifiers, 1)
	assert.Equal(t, "https://hooks.slack.test/T/B/Z", got.Notifiers[0].WebhookURL)
}

func TestResolve_EnvSecret(t *testing.T) {
	t.Setenv("RUNWISP_TEST_SLACK_URL", "https://hooks.slack.test/from-env")
	cfg := config.NotifyConfig{
		Notifiers: []config.NotifierSpec{{
			ID:         "ops",
			Type:       "slack",
			WebhookURL: "${RUNWISP_TEST_SLACK_URL}",
		}},
	}
	got, err := Resolve(cfg, t.TempDir(), render.TemplateContext{})
	require.NoError(t, err)
	require.Len(t, got.Notifiers, 1)
	assert.Equal(t, "https://hooks.slack.test/from-env", got.Notifiers[0].WebhookURL)
}

func TestResolve_EnvSecretMissing(t *testing.T) {
	t.Setenv("RUNWISP_TEST_MISSING_URL", "")
	require.NoError(t, os.Unsetenv("RUNWISP_TEST_MISSING_URL"))
	cfg := config.NotifyConfig{
		Notifiers: []config.NotifierSpec{{
			ID:         "ops",
			Type:       "slack",
			WebhookURL: "${RUNWISP_TEST_MISSING_URL}",
		}},
	}
	_, err := Resolve(cfg, t.TempDir(), render.TemplateContext{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "RUNWISP_TEST_MISSING_URL")
	assert.Contains(t, err.Error(), `notifier "ops"`)
}

func TestResolve_FileSecretRelativeToDataDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "slack.url"), []byte("https://hooks.slack.test/from-file\n"), 0o600))

	cfg := config.NotifyConfig{
		Notifiers: []config.NotifierSpec{{
			ID:         "ops",
			Type:       "slack",
			WebhookURL: "${file:slack.url}",
		}},
	}
	got, err := Resolve(cfg, dir, render.TemplateContext{})
	require.NoError(t, err)
	require.Len(t, got.Notifiers, 1)
	assert.Equal(t, "https://hooks.slack.test/from-file", got.Notifiers[0].WebhookURL)
}

func TestResolve_FileSecretAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, "secret.txt")
	require.NoError(t, os.WriteFile(abs, []byte("absolute-token\n"), 0o600))

	cfg := config.NotifyConfig{
		Notifiers: []config.NotifierSpec{{
			ID:       "tg",
			Type:     "telegram",
			BotToken: "${file:" + abs + "}",
			ChatID:   "-100123",
		}},
	}
	got, err := Resolve(cfg, t.TempDir(), render.TemplateContext{})
	require.NoError(t, err)
	require.Len(t, got.Notifiers, 1)
	assert.Equal(t, "absolute-token", got.Notifiers[0].BotToken)
}

func TestResolve_SMTP_PasswordViaEnv(t *testing.T) {
	t.Setenv("RUNWISP_TEST_SMTP_PASSWORD", "shh-from-env")
	cfg := config.NotifyConfig{
		Notifiers: []config.NotifierSpec{{
			ID:         "email-ops",
			Type:       "smtp",
			Host:       "smtp.example.com",
			Port:       587,
			Username:   "apikey",
			Password:   "${RUNWISP_TEST_SMTP_PASSWORD}",
			From:       "runwisp@example.com",
			Recipients: []string{"ops@example.com"},
		}},
	}
	got, err := Resolve(cfg, t.TempDir(), render.TemplateContext{})
	require.NoError(t, err)
	require.Len(t, got.Notifiers, 1)
	n := got.Notifiers[0]
	assert.Equal(t, "shh-from-env", n.Password)
	assert.Equal(t, "smtp.example.com", n.Host)
	assert.Equal(t, []string{"ops@example.com"}, n.Recipients)
}

func TestResolve_SMTP_PasswordViaFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "smtp.pw"), []byte("from-file\n"), 0o600))
	cfg := config.NotifyConfig{
		Notifiers: []config.NotifierSpec{{
			ID:         "email-ops",
			Type:       "smtp",
			Host:       "smtp.example.com",
			Port:       587,
			Username:   "apikey",
			Password:   "${file:smtp.pw}",
			From:       "runwisp@example.com",
			Recipients: []string{"ops@example.com"},
		}},
	}
	got, err := Resolve(cfg, dir, render.TemplateContext{})
	require.NoError(t, err)
	require.Len(t, got.Notifiers, 1)
	assert.Equal(t, "from-file", got.Notifiers[0].Password)
}

func TestResolve_SMTP_InlinePassword(t *testing.T) {
	cfg := config.NotifyConfig{
		Notifiers: []config.NotifierSpec{{
			ID:         "email-ops",
			Type:       "smtp",
			Host:       "smtp.example.com",
			Port:       587,
			Username:   "apikey",
			Password:   "inline-secret",
			From:       "runwisp@example.com",
			Recipients: []string{"ops@example.com"},
		}},
	}
	got, err := Resolve(cfg, t.TempDir(), render.TemplateContext{})
	require.NoError(t, err)
	assert.Equal(t, "inline-secret", got.Notifiers[0].Password)
}

func TestResolve_SMTP_AuthlessRelay(t *testing.T) {
	cfg := config.NotifyConfig{
		Notifiers: []config.NotifierSpec{{
			ID:         "email-local",
			Type:       "smtp",
			Host:       "127.0.0.1",
			Port:       25,
			From:       "runwisp@example.com",
			Recipients: []string{"ops@example.com"},
		}},
	}
	got, err := Resolve(cfg, t.TempDir(), render.TemplateContext{})
	require.NoError(t, err)
	require.Len(t, got.Notifiers, 1)
	assert.Empty(t, got.Notifiers[0].Password, "auth-less relay must resolve with empty password")
	assert.Equal(t, "127.0.0.1", got.Notifiers[0].Host)
}

func TestResolve_CompiledRulePredicates(t *testing.T) {
	cfg := config.NotifyConfig{
		Notifiers: []config.NotifierSpec{{
			ID:         "ops",
			Type:       "slack",
			WebhookURL: "https://hooks.slack.test/T/B/Z",
		}},
		Routes: []config.NotificationRoute{{
			Kinds:      []string{"run.failed"},
			Severity:   "warn",
			TaskGlob:   "backup-*",
			NotifierID: []string{"ops"},
		}},
	}
	got, err := Resolve(cfg, t.TempDir(), render.TemplateContext{})
	require.NoError(t, err)
	require.Len(t, got.Rules, 1)
	require.Equal(t, []string{"ops"}, got.Rules[0].ActionIDs)

	matches := got.Rules[0].Match
	require.NotNil(t, matches)

	yes := &notify.Event{Kind: notify.KindRunFailed, Severity: notify.SevError, TaskName: "backup-db"}
	assert.True(t, matches(yes))

	wrongKind := &notify.Event{Kind: notify.KindRunSucceeded, Severity: notify.SevInfo, TaskName: "backup-db"}
	assert.False(t, matches(wrongKind))

	wrongSev := &notify.Event{Kind: notify.KindRunFailed, Severity: notify.SevInfo, TaskName: "backup-db"}
	assert.False(t, matches(wrongSev))

	wrongTask := &notify.Event{Kind: notify.KindRunFailed, Severity: notify.SevError, TaskName: "etl-1"}
	assert.False(t, matches(wrongTask))
}
