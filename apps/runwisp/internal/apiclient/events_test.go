// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package apiclient

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadSSEFrames(t *testing.T) {
	body := "id: 7\nevent: run.created\ndata: {\"a\":1}\n\n" +
		": ping comment\nretry: 100\n\n" + // no data: not a frame
		"event: multi\ndata: one\ndata: two\n\n" +
		"event: cut\ndata: unterminated" // no blank line: never completed

	var got []sseFrame
	require.NoError(t, readSSEFrames(strings.NewReader(body), func(f sseFrame) bool {
		got = append(got, f)
		return true
	}))
	assert.Equal(t, []sseFrame{
		{event: "run.created", id: "7", data: `{"a":1}`},
		{event: "multi", data: "one\ntwo"},
	}, got)
}

func TestStreamRunEvents_RateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	_, err := New(srv.URL, "").StreamRunEvents(t.Context(), "")
	require.ErrorIs(t, err, ErrRateLimited)
}

func TestParseLogStreamFrame(t *testing.T) {
	t.Run("empty event name parses as line", func(t *testing.T) {
		msg, ok := parseLogStreamFrame("", `{"stream":"stdout","text":"hello"}`)
		require.True(t, ok)
		assert.Equal(t, LogStreamMsgKindLine, msg.Kind)
		assert.Equal(t, "stdout", msg.Line.Stream)
		assert.Equal(t, "hello", msg.Line.Text)
	})

	t.Run("explicit line event parses as line", func(t *testing.T) {
		msg, ok := parseLogStreamFrame("line", `{"n":5,"ts":1000,"stream":"stderr","text":"err msg"}`)
		require.True(t, ok)
		assert.Equal(t, LogStreamMsgKindLine, msg.Kind)
		assert.Equal(t, "stderr", msg.Line.Stream)
		assert.Equal(t, "err msg", msg.Line.Text)
		assert.Equal(t, int64(5), msg.Line.N)
	})

	t.Run("line event with invalid JSON returns false", func(t *testing.T) {
		_, ok := parseLogStreamFrame("line", `not-json`)
		assert.False(t, ok)
	})

	t.Run("empty event with invalid JSON returns false", func(t *testing.T) {
		_, ok := parseLogStreamFrame("", `{bad`)
		assert.False(t, ok)
	})

	t.Run("rotated event with valid data", func(t *testing.T) {
		msg, ok := parseLogStreamFrame("rotated", `{"firstAvailable":42}`)
		require.True(t, ok)
		assert.Equal(t, LogStreamMsgKindRotated, msg.Kind)
		assert.Equal(t, int64(42), msg.Rotated.FirstAvailable)
	})

	t.Run("rotated event with invalid JSON returns false", func(t *testing.T) {
		_, ok := parseLogStreamFrame("rotated", `bad`)
		assert.False(t, ok)
	})

	t.Run("dropped event with valid data", func(t *testing.T) {
		msg, ok := parseLogStreamFrame("dropped", `{"after":10,"count":5}`)
		require.True(t, ok)
		assert.Equal(t, LogStreamMsgKindDropped, msg.Kind)
		assert.Equal(t, int64(10), msg.Dropped.After)
		assert.Equal(t, int64(5), msg.Dropped.Count)
	})

	t.Run("dropped event with invalid JSON returns false", func(t *testing.T) {
		_, ok := parseLogStreamFrame("dropped", `bad`)
		assert.False(t, ok)
	})

	t.Run("done event with valid data", func(t *testing.T) {
		msg, ok := parseLogStreamFrame("done", `{"finalLine":100,"status":"ended"}`)
		require.True(t, ok)
		assert.Equal(t, LogStreamMsgKindDone, msg.Kind)
		assert.Equal(t, int64(100), msg.Done.FinalLine)
		assert.Equal(t, "ended", msg.Done.Status)
	})

	t.Run("done event with invalid JSON returns false", func(t *testing.T) {
		_, ok := parseLogStreamFrame("done", `bad`)
		assert.False(t, ok)
	})

	t.Run("unknown event returns false", func(t *testing.T) {
		_, ok := parseLogStreamFrame("unknown", `{"some":"data"}`)
		assert.False(t, ok)
	})

	t.Run("unrecognised event name returns false", func(t *testing.T) {
		_, ok := parseLogStreamFrame("heartbeat", `{}`)
		assert.False(t, ok)
	})
}
