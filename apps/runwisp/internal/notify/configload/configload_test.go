// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package configload

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/render"
)

func TestResolve_Slack(t *testing.T) {
	cfg := config.NotifyConfig{
		Notifiers: []config.NotifierSpec{{
			ID:         "ops",
			Type:       "slack",
			WebhookURL: "https://hooks.slack.test/T/B/Z",
		}},
	}
	got, err := Resolve(cfg, render.TemplateContext{})
	require.NoError(t, err)
	require.Len(t, got.Notifiers, 1)
	assert.Equal(t, "https://hooks.slack.test/T/B/Z", got.Notifiers[0].WebhookURL)
}

func TestResolve_Discord(t *testing.T) {
	cfg := config.NotifyConfig{
		Notifiers: []config.NotifierSpec{{
			ID:         "discord-ops",
			Type:       "discord",
			WebhookURL: "https://discord.com/api/webhooks/123/token",
		}},
	}
	got, err := Resolve(cfg, render.TemplateContext{})
	require.NoError(t, err)
	require.Len(t, got.Notifiers, 1)
	assert.Equal(t, "https://discord.com/api/webhooks/123/token", got.Notifiers[0].WebhookURL)
}

func TestResolve_Telegram(t *testing.T) {
	cfg := config.NotifyConfig{
		Notifiers: []config.NotifierSpec{{
			ID:       "tg",
			Type:     "telegram",
			BotToken: "123456:token",
			ChatID:   "-100123",
		}},
	}
	got, err := Resolve(cfg, render.TemplateContext{})
	require.NoError(t, err)
	require.Len(t, got.Notifiers, 1)
	assert.Equal(t, "123456:token", got.Notifiers[0].BotToken)
	assert.Equal(t, "-100123", got.Notifiers[0].ChatID)
}

func TestResolve_SMTP_Password(t *testing.T) {
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
	got, err := Resolve(cfg, render.TemplateContext{})
	require.NoError(t, err)
	require.Len(t, got.Notifiers, 1)
	n := got.Notifiers[0]
	assert.Equal(t, "inline-secret", n.Password)
	assert.Equal(t, "smtp.example.com", n.Host)
	assert.Equal(t, []string{"ops@example.com"}, n.Recipients)
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
	got, err := Resolve(cfg, render.TemplateContext{})
	require.NoError(t, err)
	require.Len(t, got.Notifiers, 1)
	assert.Empty(t, got.Notifiers[0].Password, "auth-less relay must resolve with empty password")
	assert.Equal(t, "127.0.0.1", got.Notifiers[0].Host)
}

func TestResolve_Webhook(t *testing.T) {
	cfg := config.NotifyConfig{
		Notifiers: []config.NotifierSpec{{
			ID:      "my-hook",
			Type:    "webhook",
			URL:     "https://example.com/hook",
			Headers: map[string]string{"Authorization": "Bearer tok"},
		}},
	}
	got, err := Resolve(cfg, render.TemplateContext{})
	require.NoError(t, err)
	require.Len(t, got.Notifiers, 1)
	assert.Equal(t, "https://example.com/hook", got.Notifiers[0].URL)
	assert.Equal(t, "Bearer tok", got.Notifiers[0].Headers["Authorization"])
}

func TestResolve_WebhookNoHeaders(t *testing.T) {
	cfg := config.NotifyConfig{
		Notifiers: []config.NotifierSpec{{
			ID:   "my-hook",
			Type: "webhook",
			URL:  "https://example.com/hook",
		}},
	}
	got, err := Resolve(cfg, render.TemplateContext{})
	require.NoError(t, err)
	require.Len(t, got.Notifiers, 1)
	assert.Nil(t, got.Notifiers[0].Headers)
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
	got, err := Resolve(cfg, render.TemplateContext{})
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
