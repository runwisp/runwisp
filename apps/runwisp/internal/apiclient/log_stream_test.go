// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package apiclient

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStreamLogLines_ParsesEvents wires up a synthetic SSE feed that produces
// every flavour of log-stream message and verifies the client decodes them
// correctly. We don't rely on the channel closing — defer cancel() tears the
// background goroutine down once we have read what we need.
func TestStreamLogLines_ParsesEvents(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/tasks/t/runs/r/log/stream", r.URL.Path)
		assert.Equal(t, "0", r.URL.Query().Get("from"))
		assert.Equal(t, "100", r.URL.Query().Get("replay_limit"))

		w.Header().Set("Content-Type", "text/event-stream")

		// Event 1: line
		fmt.Fprintln(w, "event: line")
		fmt.Fprintln(w, `data: {"n":1,"stream":"stdout","text":"hello"}`)
		fmt.Fprintln(w)

		// Event 2: rotated
		fmt.Fprintln(w, "event: rotated")
		fmt.Fprintln(w, `data: {"first_available":42}`)
		fmt.Fprintln(w)

		// Event 3: dropped
		fmt.Fprintln(w, "event: dropped")
		fmt.Fprintln(w, `data: {"after":10,"count":5}`)
		fmt.Fprintln(w)

		// Event 4: id-prefix line is ignored, then done
		fmt.Fprintln(w, "id: 42")
		fmt.Fprintln(w, "event: done")
		fmt.Fprintln(w, `data: {"final_line":99}`)
		fmt.Fprintln(w)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := c.StreamLogLines(ctx, "t", "r", StreamLogOpts{FromLine: 0, ReplayLimit: 100})
	require.NoError(t, err)

	read := func() LogStreamMsg {
		select {
		case msg := <-ch:
			return msg
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for log stream msg")
			return LogStreamMsg{}
		}
	}

	first := read()
	assert.Equal(t, LogStreamMsgKindLine, first.Kind)
	assert.Equal(t, int64(1), first.Line.N)
	assert.Equal(t, "hello", first.Line.Text)

	second := read()
	assert.Equal(t, LogStreamMsgKindRotated, second.Kind)
	assert.Equal(t, int64(42), second.Rotated.FirstAvailable)

	third := read()
	assert.Equal(t, LogStreamMsgKindDropped, third.Kind)
	assert.Equal(t, int64(10), third.Dropped.After)
	assert.Equal(t, int64(5), third.Dropped.Count)

	fourth := read()
	assert.Equal(t, LogStreamMsgKindDone, fourth.Kind)
	assert.Equal(t, int64(99), fourth.Done.FinalLine)
}

// TestStreamLogLines_NoReplayLimitOmitsQueryParam asserts the URL excludes
// the replay_limit param when callers leave it at the default — the server
// then chooses.
func TestStreamLogLines_NoReplayLimitOmitsQueryParam(t *testing.T) {
	gotQuery := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, "event: done")
		fmt.Fprintln(w, `data: {"final_line":0}`)
		fmt.Fprintln(w)
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := c.StreamLogLines(ctx, "t", "r", StreamLogOpts{FromLine: -100})
	require.NoError(t, err)

	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatal("expected at least one event before timeout")
	}

	assert.NotContains(t, gotQuery, "replay_limit", "default ReplayLimit must not appear in query")
	assert.True(t, strings.Contains(gotQuery, "from=-100"))
}
