// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package pushover

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/notify"
	"github.com/runwisp/runwisp/internal/notify/testutil"
)

var failed = &notify.Event{Kind: notify.KindRunFailed, Severity: notify.SevError, TaskName: "backup-db", Timestamp: time.Now().UTC()}

func TestPushover_PostsTokenAndUserInBody(t *testing.T) {
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		assert.NoError(t, json.Unmarshal(b, &payload))
		_, _ = w.Write([]byte(`{"status":1,"request":"x"}`))
	}))
	defer srv.Close()

	ch, err := New(Config{
		ID: "po", Token: "apptoken", User: "userkey", Endpoint: srv.URL,
		Renderer:  testutil.NewTestRenderer(t, "pushover", "application/json"),
		Transport: testutil.NewFastTransport(),
	})
	require.NoError(t, err)
	require.NoError(t, ch.Execute(context.Background(), failed))

	assert.Equal(t, "apptoken", payload["token"])
	assert.Equal(t, "userkey", payload["user"])
	assert.Contains(t, payload["title"], "backup-db")
	assert.EqualValues(t, 0, payload["priority"])
}

func TestPushover_PermanentOn4xxWithoutLeakingKeys(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"user":"invalid","errors":["user identifier is invalid"],"status":0}`))
	}))
	defer srv.Close()

	ch, err := New(Config{ID: "po", Token: "apptoken", User: "userkey", Endpoint: srv.URL,
		Renderer: testutil.NewTestRenderer(t, "pushover", "application/json"), Transport: testutil.NewFastTransport()})
	require.NoError(t, err)
	err = ch.Execute(context.Background(), failed)
	require.Error(t, err)
	assert.EqualValues(t, 1, hits.Load(), "4xx must not be retried")
	assert.NotContains(t, err.Error(), "apptoken")
	assert.NotContains(t, err.Error(), "userkey")
}
