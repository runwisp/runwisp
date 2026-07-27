// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package discord

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/testutil"
)

func TestDiscord_PostsEmbedJSON(t *testing.T) {
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		b, _ := io.ReadAll(r.Body)
		receivedBody = b
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	ch, err := New(Config{
		ID:         "discord-ops",
		WebhookURL: srv.URL,
		Renderer:   testutil.NewTestRenderer(t, "discord", "application/json"),
		Transport:  testutil.NewFastTransport(),
	})
	require.NoError(t, err)

	require.NoError(t, ch.Execute(context.Background(), &notify.Event{
		Kind: notify.KindRunFailed, Severity: notify.SevError,
		TaskName: "backup-db", Reason: "exit 1",
		Timestamp: time.Now().UTC(),
	}))

	require.NotEmpty(t, receivedBody)
	var payload struct {
		Embeds []struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Color       int    `json:"color"`
			Footer      struct {
				Text string `json:"text"`
			} `json:"footer"`
		} `json:"embeds"`
		AllowedMentions struct {
			Parse []string `json:"parse"`
		} `json:"allowed_mentions"`
	}
	require.NoError(t, json.Unmarshal(receivedBody, &payload))
	require.Len(t, payload.Embeds, 1)
	embed := payload.Embeds[0]
	assert.Contains(t, embed.Title, "backup-db")
	assert.Contains(t, embed.Title, "failed")
	assert.Equal(t, 15548997, embed.Color, "run.failed must render red")
	assert.Contains(t, embed.Footer.Text, "from runwisp")
	assert.NotNil(t, payload.AllowedMentions.Parse, "allowed_mentions.parse must be present (and empty) to suppress pings")
	assert.Empty(t, payload.AllowedMentions.Parse)
}

func TestDiscord_SucceededRendersGreen(t *testing.T) {
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		receivedBody = b
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	ch, err := New(Config{
		ID:         "discord-ops",
		WebhookURL: srv.URL,
		Renderer:   testutil.NewTestRenderer(t, "discord", "application/json"),
		Transport:  testutil.NewFastTransport(),
	})
	require.NoError(t, err)

	require.NoError(t, ch.Execute(context.Background(), &notify.Event{
		Kind: notify.KindRunSucceeded, Severity: notify.SevInfo, TaskName: "deploy",
		Timestamp: time.Now().UTC(),
	}))

	var payload struct {
		Embeds []struct {
			Color int `json:"color"`
		} `json:"embeds"`
	}
	require.NoError(t, json.Unmarshal(receivedBody, &payload))
	require.Len(t, payload.Embeds, 1)
	assert.Equal(t, 5763719, payload.Embeds[0].Color, "run.succeeded must render green")
}

func TestDiscord_RetriesOn429WithDiscordBody(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"You are being rate limited.","retry_after":0.01,"global":false}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	ch, err := New(Config{
		ID:         "discord-ops",
		WebhookURL: srv.URL,
		Renderer:   testutil.NewTestRenderer(t, "discord", "application/json"),
		Transport:  testutil.NewFastTransport(),
	})
	require.NoError(t, err)

	require.NoError(t, ch.Execute(context.Background(), &notify.Event{
		Kind: notify.KindRunFailed, Severity: notify.SevError, TaskName: "x",
		Timestamp: time.Now().UTC(),
	}))
	assert.EqualValues(t, 2, hits.Load(), "must retry after the 429 and then succeed")
}

func TestDiscord_RedactsWebhookURLInError(t *testing.T) {
	ch, err := New(Config{
		ID:         "discord-ops",
		WebhookURL: "http://127.0.0.1:1/api/webhooks/123/SECRETTOKEN",
		Renderer:   testutil.NewTestRenderer(t, "discord", "application/json"),
		Transport:  testutil.NewFastTransport(),
	})
	require.NoError(t, err)

	err = ch.Execute(context.Background(), &notify.Event{
		Kind: notify.KindRunFailed, Severity: notify.SevError, TaskName: "x",
	})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRETTOKEN", "webhook URL must not leak into error")
	assert.True(t, strings.HasPrefix(err.Error(), "discord:discord-ops:"), "error must be labelled as the discord channel")
}

func TestDiscord_MissingURLOrRendererErrors(t *testing.T) {
	_, err := New(Config{ID: "discord-ops", Renderer: testutil.NewTestRenderer(t, "discord", "application/json")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `discord channel "discord-ops": url is required`)

	_, err = New(Config{ID: "discord-ops", WebhookURL: "https://discord.com/api/webhooks/1/t"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `discord channel "discord-ops": renderer is required`)
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		name string
		body string
		want time.Duration
	}{
		{"fractional seconds", `{"message":"You are being rate limited.","retry_after":64.57,"global":false}`, 64570 * time.Millisecond},
		{"whole seconds", `{"retry_after":2}`, 2 * time.Second},
		{"zero", `{"retry_after":0}`, 0},
		{"negative", `{"retry_after":-1}`, 0},
		{"not discord shaped", `{"error":"nope"}`, 0},
		{"not json", `whoops`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, parseRetryAfter([]byte(tt.body)))
		})
	}
}
