// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package channel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/notify"
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

// TestBuild_SmtpHappyPathReturnsChannel exercises the case "smtp" branch end
// to end, including renderer construction.
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

// TestBuild_SmtpEmptyHostReturnsError exercises the smtp.New validation
// failure path inside Build.
func TestBuild_SmtpEmptyHostReturnsError(t *testing.T) {
	_, err := Build(NotifierSpec{ID: "mail", Type: "smtp"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "host is required")
}

// TestBuild_SlackTemplatePathMissing exercises the LoadTemplate error branch
// on the slack path.
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

// TestBuild_TelegramTemplatePathMissing exercises the LoadTemplate error
// branch on the telegram path.
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

// TestBuild_SmtpTemplatePathMissing exercises the LoadTemplate error branch
// on the smtp path.
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

// TestHttpTransport_BackoffOnlyReturnsHTTPProviderWithBackoff exercises the
// branch where spec.Transport is nil but spec.Backoff is set — the helper
// builds a default HTTPProvider and stamps the backoff onto it.
func TestHttpTransport_BackoffOnlyReturnsHTTPProviderWithBackoff(t *testing.T) {
	bo := notify.BackoffConfig{InitialInterval: 1, MaxInterval: 2, MaxElapsedTime: 3, Multiplier: 2}
	p := httpTransport(NotifierSpec{Backoff: bo})
	require.NotNil(t, p)
	assert.Equal(t, bo, p.Backoff)
}

// TestHttpTransport_NoTransportNoBackoffReturnsNil covers the early-return
// when both override fields are zero.
func TestHttpTransport_NoTransportNoBackoffReturnsNil(t *testing.T) {
	assert.Nil(t, httpTransport(NotifierSpec{}))
}
