// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package channel

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
	"github.com/runwisp/runwisp/internal/notify/channel/webhook"
	notifytest "github.com/runwisp/runwisp/internal/notify/testutil"
)

var failedEvent = &notify.Event{Kind: notify.KindRunFailed, Severity: notify.SevError, TaskName: "backup-db", Reason: "exit 1", Timestamp: time.Now().UTC()}

// capturedRequest is what a webhook-kind channel sent to the test server.
type capturedRequest struct {
	method, path string
	header       http.Header
	payload      map[string]any
}

// captureServer records the last request and answers with status.
func captureServer(t *testing.T, status int) (*httptest.Server, *capturedRequest) {
	t.Helper()
	got := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.method, got.path, got.header = r.Method, r.URL.Path, r.Header.Clone()
		b, _ := io.ReadAll(r.Body)
		assert.NoError(t, json.Unmarshal(b, &got.payload))
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

// buildFast builds spec with the fast test transport.
func buildFast(t *testing.T, spec NotifierSpec) notify.Channel {
	t.Helper()
	spec.Transport = notifytest.NewFastTransport()
	ch, err := Build(spec)
	require.NoError(t, err)
	return ch
}

func TestSlack_PostsBlockKitJSON(t *testing.T) {
	srv, got := captureServer(t, http.StatusOK)
	ch := buildFast(t, NotifierSpec{ID: "ops", Type: "slack", WebhookURL: srv.URL})
	require.NoError(t, ch.Execute(context.Background(), failedEvent))

	assert.Equal(t, http.MethodPost, got.method)
	assert.Equal(t, "application/json", got.header.Get("Content-Type"))
	assert.Contains(t, got.payload, "blocks")
	assert.NotContains(t, got.payload, "channel", "no override means no channel key")
}

func TestSlack_ChannelOverrideIncludedWhenSet(t *testing.T) {
	srv, got := captureServer(t, http.StatusOK)
	ch := buildFast(t, NotifierSpec{ID: "ops", Type: "slack", WebhookURL: srv.URL, SlackChannel: "#alerts"})
	require.NoError(t, ch.Execute(context.Background(), failedEvent))
	assert.Equal(t, "#alerts", got.payload["channel"])
}

func TestSlack_RedactsURLInError(t *testing.T) {
	ch := buildFast(t, NotifierSpec{ID: "ops", Type: "slack", WebhookURL: "http://127.0.0.1:1/hooks/B0XXXSECRET"})
	err := ch.Execute(context.Background(), failedEvent)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "B0XXXSECRET", "URL must not leak into error")
	assert.True(t, strings.HasPrefix(err.Error(), "slack:ops:"), "error must start with redacted channel id")
}

func TestDiscord_PostsEmbedJSON(t *testing.T) {
	srv, got := captureServer(t, http.StatusNoContent)
	ch := buildFast(t, NotifierSpec{ID: "discord-ops", Type: "discord", WebhookURL: srv.URL})
	require.NoError(t, ch.Execute(context.Background(), failedEvent))

	assert.Equal(t, http.MethodPost, got.method)
	assert.Equal(t, "application/json", got.header.Get("Content-Type"))
	raw, err := json.Marshal(got.payload)
	require.NoError(t, err)
	var payload struct {
		Embeds []struct {
			Title  string `json:"title"`
			Color  int    `json:"color"`
			Footer struct {
				Text string `json:"text"`
			} `json:"footer"`
		} `json:"embeds"`
		AllowedMentions struct {
			Parse []string `json:"parse"`
		} `json:"allowed_mentions"`
	}
	require.NoError(t, json.Unmarshal(raw, &payload))
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
	srv, got := captureServer(t, http.StatusNoContent)
	ch := buildFast(t, NotifierSpec{ID: "discord-ops", Type: "discord", WebhookURL: srv.URL})
	require.NoError(t, ch.Execute(context.Background(), &notify.Event{
		Kind: notify.KindRunSucceeded, Severity: notify.SevInfo, TaskName: "deploy", Timestamp: time.Now().UTC(),
	}))
	embeds, ok := got.payload["embeds"].([]any)
	require.True(t, ok)
	require.Len(t, embeds, 1)
	embed, ok := embeds[0].(map[string]any)
	require.True(t, ok)
	assert.EqualValues(t, 5763719, embed["color"], "run.succeeded must render green")
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

	ch := buildFast(t, NotifierSpec{ID: "discord-ops", Type: "discord", WebhookURL: srv.URL})
	require.NoError(t, ch.Execute(context.Background(), failedEvent))
	assert.EqualValues(t, 2, hits.Load(), "must retry after the 429 and then succeed")
}

func TestDiscord_InstallsRetryAfterParserOnDefaultTransport(t *testing.T) {
	cfg := webhookConfig(NotifierSpec{ID: "discord-ops", Type: "discord", WebhookURL: "https://discord.test/x"})
	require.NotNil(t, cfg.Transport)
	assert.NotNil(t, cfg.Transport.Body429Fn)
}

func TestDiscord_RedactsWebhookURLInError(t *testing.T) {
	ch := buildFast(t, NotifierSpec{ID: "discord-ops", Type: "discord", WebhookURL: "http://127.0.0.1:1/api/webhooks/123/SECRETTOKEN"})
	err := ch.Execute(context.Background(), failedEvent)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRETTOKEN", "webhook URL must not leak into error")
	assert.True(t, strings.HasPrefix(err.Error(), "discord:discord-ops:"), "error must be labelled as the discord channel")
}

func TestParseDiscordRetryAfter(t *testing.T) {
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
			assert.Equal(t, tt.want, parseDiscordRetryAfter([]byte(tt.body)))
		})
	}
}

func TestGotify_PostsMessageWithAppTokenHeader(t *testing.T) {
	srv, got := captureServer(t, http.StatusOK)
	ch := buildFast(t, NotifierSpec{ID: "gotify", Type: "gotify", URL: srv.URL + "/sub/", Token: "Aapp-token"})
	require.NoError(t, ch.Execute(context.Background(), failedEvent))

	assert.Equal(t, "/sub/message", got.path, "a server under a sub-path keeps it")
	assert.Equal(t, "Aapp-token", got.header.Get("X-Gotify-Key"))
	assert.Contains(t, got.payload["title"], "backup-db")
	assert.EqualValues(t, 8, got.payload["priority"])
}

func TestGotify_MissingURLErrors(t *testing.T) {
	_, err := Build(NotifierSpec{ID: "gotify", Type: "gotify", Token: "t"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `gotify channel "gotify": url is required`)
}

func TestNtfy_PublishesJSONToServerRootWithTopicAndToken(t *testing.T) {
	srv, got := captureServer(t, http.StatusOK)
	ch := buildFast(t, NotifierSpec{ID: "phone", Type: "ntfy", URL: srv.URL + "/", Topic: "alerts", Token: "tk_secret"})
	require.NoError(t, ch.Execute(context.Background(), failedEvent))

	assert.Equal(t, "/", got.path, "JSON publishes go to the server root, not /<topic>")
	assert.Equal(t, "Bearer tk_secret", got.header.Get("Authorization"))
	assert.Equal(t, "alerts", got.payload["topic"])
	assert.Contains(t, got.payload["title"], "backup-db")
	assert.EqualValues(t, 4, got.payload["priority"])
}

func TestNtfy_NoTokenSendsNoAuthHeader(t *testing.T) {
	srv, got := captureServer(t, http.StatusOK)
	ch := buildFast(t, NotifierSpec{ID: "phone", Type: "ntfy", URL: srv.URL, Topic: "alerts"})
	require.NoError(t, ch.Execute(context.Background(), failedEvent))
	assert.Empty(t, got.header.Get("Authorization"))
}

func TestNtfy_DefaultsToPublicServer(t *testing.T) {
	assert.Equal(t, "https://ntfy.sh", webhookConfig(NotifierSpec{Type: "ntfy", Topic: "alerts"}).URL)
}

func TestNtfy_PermanentOn4xxWithoutLeakingToken(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":40301,"error":"forbidden"}`))
	}))
	defer srv.Close()

	ch := buildFast(t, NotifierSpec{ID: "phone", Type: "ntfy", URL: srv.URL, Topic: "alerts", Token: "tk_secret"})
	err := ch.Execute(context.Background(), failedEvent)
	require.Error(t, err)
	assert.EqualValues(t, 1, hits.Load(), "4xx must not be retried")
	assert.NotContains(t, err.Error(), "tk_secret")
	assert.Contains(t, err.Error(), "ntfy:phone:")
}

// pushoverAt builds a Pushover channel aimed at url instead of the real API.
func pushoverAt(t *testing.T, url string) notify.Channel {
	t.Helper()
	cfg := webhookConfig(NotifierSpec{ID: "po", Type: "pushover", Token: "apptoken", User: "userkey"})
	require.Equal(t, pushoverEndpoint, cfg.URL)
	cfg.URL = url
	cfg.Renderer = notifytest.NewTestRenderer(t, "pushover", "application/json")
	cfg.Transport = notifytest.NewFastTransport()
	ch, err := webhook.New(cfg)
	require.NoError(t, err)
	return ch
}

func TestPushover_PostsTokenAndUserInBody(t *testing.T) {
	srv, got := captureServer(t, http.StatusOK)
	require.NoError(t, pushoverAt(t, srv.URL).Execute(context.Background(), failedEvent))

	assert.Equal(t, "apptoken", got.payload["token"])
	assert.Equal(t, "userkey", got.payload["user"])
	assert.Contains(t, got.payload["title"], "backup-db")
	assert.EqualValues(t, 0, got.payload["priority"])
}

func TestPushover_PermanentOn4xxWithoutLeakingKeys(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"user":"invalid","errors":["user identifier is invalid"],"status":0}`))
	}))
	defer srv.Close()

	err := pushoverAt(t, srv.URL).Execute(context.Background(), failedEvent)
	require.Error(t, err)
	assert.EqualValues(t, 1, hits.Load(), "4xx must not be retried")
	assert.NotContains(t, err.Error(), "apptoken")
	assert.NotContains(t, err.Error(), "userkey")
}
