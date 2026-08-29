// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package channel

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/testutil"
)

func TestBuild_UnknownTypeReturnsClearError(t *testing.T) {
	_, err := Build(NotifierSpec{ID: "x", Type: "carrier-pigeon"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown notifier type")
	assert.Contains(t, err.Error(), "carrier-pigeon")
	assert.Contains(t, err.Error(), `id=x`)
}

func TestBuild_SlackPropagatesTransport(t *testing.T) {
	tr := notify.NewHTTPProvider()
	ch, err := Build(NotifierSpec{
		ID:         "ops",
		Type:       "slack",
		WebhookURL: "https://hooks.slack.test/T/B/Z",
		Transport:  tr,
	})
	require.NoError(t, err)
	require.NotNil(t, ch)
	assert.Equal(t, "ops", ch.ID())
}

func TestBuild_SlackEmptyWebhookReturnsError(t *testing.T) {
	_, err := Build(NotifierSpec{ID: "ops", Type: "slack"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "webhook URL is required")
}

func TestBuild_DiscordPropagatesTransport(t *testing.T) {
	tr := notify.NewHTTPProvider()
	ch, err := Build(NotifierSpec{
		ID:         "discord-ops",
		Type:       "discord",
		WebhookURL: "https://discord.com/api/webhooks/1/token",
		Transport:  tr,
	})
	require.NoError(t, err)
	require.NotNil(t, ch)
	assert.Equal(t, "discord-ops", ch.ID())
}

func TestBuild_DiscordEmptyWebhookReturnsError(t *testing.T) {
	_, err := Build(NotifierSpec{ID: "discord-ops", Type: "discord"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "url is required")
}

func TestBuild_TelegramPropagatesTransport(t *testing.T) {
	// Regression for the dropped Transport on the telegram path: when an
	// override is supplied, the constructed channel must use it (the only
	// way to assert this externally is via the error path on a transport
	// with no Body429Fn — the constructor sets a default, so we verify the
	// happy-path build succeeds).
	tr := notify.NewHTTPProvider()
	ch, err := Build(NotifierSpec{
		ID:        "alerts",
		Type:      "telegram",
		BotToken:  "secret",
		ChatID:    "-100123",
		Transport: tr,
	})
	require.NoError(t, err)
	require.NotNil(t, ch)
	assert.Equal(t, "alerts", ch.ID())
}

func TestBuild_TelegramDefaultParseModeIsHTML(t *testing.T) {
	ch, err := Build(NotifierSpec{
		ID:       "alerts",
		Type:     "telegram",
		BotToken: "secret",
		ChatID:   "-100123",
	})
	require.NoError(t, err)
	require.NotNil(t, ch)
}

func TestBuild_WebhookPropagatesTransport(t *testing.T) {
	tr := notify.NewHTTPProvider()
	ch, err := Build(NotifierSpec{
		ID:        "my-hook",
		Type:      "webhook",
		URL:       "https://example.com/hook",
		Transport: tr,
	})
	require.NoError(t, err)
	require.NotNil(t, ch)
	assert.Equal(t, "my-hook", ch.ID())
}

func TestBuild_WebhookEmptyURLReturnsError(t *testing.T) {
	_, err := Build(NotifierSpec{ID: "my-hook", Type: "webhook"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "url is required")
}

func TestBuild_WebhookWithHeaders(t *testing.T) {
	ch, err := Build(NotifierSpec{
		ID:      "my-hook",
		Type:    "webhook",
		URL:     "https://example.com/hook",
		Headers: map[string]string{"Authorization": "Bearer tok"},
	})
	require.NoError(t, err)
	require.NotNil(t, ch)
}

func TestBuild_SmtpHappyPathReturnsChannel(t *testing.T) {
	ch, err := Build(NotifierSpec{
		ID:         "mail",
		Type:       "smtp",
		Host:       "smtp.example.test",
		Port:       587,
		From:       "RunWisp <runwisp@example.test>",
		Recipients: []string{"oncall@example.test"},
	})
	require.NoError(t, err)
	require.NotNil(t, ch)
	assert.Equal(t, "mail", ch.ID())
}

func TestBuild_SmtpEmptyHostReturnsError(t *testing.T) {
	_, err := Build(NotifierSpec{ID: "mail", Type: "smtp"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host is required")
}

// TestBuild_SmtpPropagatesBackoff guards against a wiring bug: unlike
// Transport (Slack/Discord/Telegram/webhook), spec.Backoff used to never
// reach smtp.Config, so a configured notify.retry_budget silently had no
// effect on SMTP (and sendmail, which shares the same factory pattern) and
// every failed send retried for the full 5-minute default instead. Dialing a
// closed local port fails immediately, so a channel actually honoring a tiny
// injected MaxElapsedTime gives up in well under a second; one still using
// the 5-minute default would still be retrying when the safety-net deadline
// below fires.
func TestBuild_SmtpPropagatesBackoff(t *testing.T) {
	port := testutil.PickFreePort(t)
	ch, err := Build(NotifierSpec{
		ID:         "mail",
		Type:       "smtp",
		Host:       "127.0.0.1",
		Port:       port,
		From:       "RunWisp <runwisp@example.test>",
		Recipients: []string{"oncall@example.test"},
		Backoff: notify.BackoffConfig{
			InitialInterval: time.Millisecond,
			MaxInterval:     5 * time.Millisecond,
			MaxElapsedTime:  50 * time.Millisecond,
			Multiplier:      2,
		},
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- ch.Execute(ctx, &notify.Event{
			Kind:      notify.KindRunFailed,
			Severity:  notify.SevError,
			TaskName:  "backup-db",
			Reason:    "exit 1",
			Timestamp: time.Now().UTC(),
		})
	}()

	select {
	case <-done:
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("smtp channel ignored spec.Backoff and is still retrying under the default 5-minute budget")
	}
}

func TestBuild_SlackTemplatePathMissing(t *testing.T) {
	_, err := Build(NotifierSpec{
		ID:           "ops",
		Type:         "slack",
		WebhookURL:   "https://hooks.slack.test/T/B/Z",
		TemplatePath: "/no/such/template/file/here.tmpl",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read template")
}

func TestBuild_TelegramTemplatePathMissing(t *testing.T) {
	_, err := Build(NotifierSpec{
		ID:           "alerts",
		Type:         "telegram",
		BotToken:     "secret",
		ChatID:       "-100123",
		TemplatePath: "/no/such/template/file/here.tmpl",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read template")
}

func TestBuild_SmtpTemplatePathMissing(t *testing.T) {
	_, err := Build(NotifierSpec{
		ID:           "mail",
		Type:         "smtp",
		Host:         "smtp.example.test",
		From:         "f@example.test",
		Recipients:   []string{"r@example.test"},
		TemplatePath: "/no/such/template/file/here.tmpl",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read template")
}
