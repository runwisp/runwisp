// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package gotify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/testutil"
)

func TestGotify_PostsMessageWithAppTokenHeader(t *testing.T) {
	var path, key string
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, key = r.URL.Path, r.Header.Get("X-Gotify-Key")
		b, _ := io.ReadAll(r.Body)
		assert.NoError(t, json.Unmarshal(b, &payload))
	}))
	defer srv.Close()

	ch, err := New(Config{
		ID: "gotify", URL: srv.URL + "/sub/", Token: "Aapp-token",
		Renderer:  testutil.NewTestRenderer(t, "gotify", "application/json"),
		Transport: testutil.NewFastTransport(),
	})
	require.NoError(t, err)
	require.NoError(t, ch.Execute(context.Background(), &notify.Event{
		Kind: notify.KindRunFailed, Severity: notify.SevError, TaskName: "backup-db", Timestamp: time.Now().UTC(),
	}))

	assert.Equal(t, "/sub/message", path, "a server under a sub-path keeps it")
	assert.Equal(t, "Aapp-token", key)
	assert.Contains(t, payload["title"], "backup-db")
	assert.EqualValues(t, 8, payload["priority"])
}

func TestGotify_MissingURLErrors(t *testing.T) {
	_, err := New(Config{ID: "gotify", Token: "t", Renderer: testutil.NewTestRenderer(t, "gotify", "application/json")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `gotify channel "gotify": url is required`)
}
