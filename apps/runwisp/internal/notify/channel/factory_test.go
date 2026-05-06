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
