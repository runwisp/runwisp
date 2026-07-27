// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFinishExecJSON_ReusesFetchedRunWithoutRefetch is the regression test for
// Bug G: exec --json must report the exit code already captured during the
// follow, not do a second GetRun that could fail and mask a real success as a
// spurious failure. The client points at a daemon whose every GetRun 500s; a
// provided terminal run must be reused so the doc still reports exit 0 / success.
func TestFinishExecJSON_ReusesFetchedRunWithoutRefetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	client := apiclient.New(srv.URL, "")

	reason := model.ReasonSuccess
	final := &model.Run{ID: "run-1", TaskName: "alpha", Status: model.PhaseEnded, EndReason: &reason, ExitCode: 0}

	var buf bytes.Buffer
	err := finishExecJSON(&buf, client, "alpha", "run-1", final)
	require.NoError(t, err, "a terminal run already in hand must be reused, never re-fetched")

	var doc execJSONDoc
	require.NoError(t, json.Unmarshal(buf.Bytes(), &doc))
	require.NotNil(t, doc.ExitCode)
	assert.Equal(t, 0, *doc.ExitCode, "the real exit code must survive a failing trailing fetch")
	assert.False(t, doc.Failed, "a successful run must not be reported as failed")
}

// captureStdout redirects os.Stdout for the duration of fn and returns what was
// written. followRun prints streamed log lines straight to os.Stdout, so this is
// how we assert the run's output actually reached the operator.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = old })

	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()

	fn()
	require.NoError(t, w.Close())
	os.Stdout = old
	return <-done
}

// TestFollowRun_RetriesEmptyStreamUntilRunIsStreamable reproduces the flaky-exec
// bug: a just-triggered run is handed back before its row is durably persisted
// (persistence is async), so the log-stream SSE handler can't resolve it yet and
// closes the connection with a bare 200 — no line, no Done. followRun must not
// treat that empty stream as "the run produced nothing and ended"; it must keep
// re-opening until the row lands, or it would exit 0 having silently swallowed
// the run's output.
func TestFollowRun_RetriesEmptyStreamUntilRunIsStreamable(t *testing.T) {
	var streamHits atomic.Int32
	const emptyStreamsBeforeReady = 2

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/log/stream"):
			hit := streamHits.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			// The first few opens mimic a not-yet-persisted run: the server
			// resolves nothing and returns an empty stream.
			if int(hit) <= emptyStreamsBeforeReady {
				return
			}
			// Once the row has landed, the stream replays the run off disk.
			fmt.Fprint(w, "event: line\ndata: {\"n\":0,\"stream\":\"stdout\",\"text\":\"alpha-line-1\"}\n\n")
			fmt.Fprint(w, "event: done\ndata: {\"final_line\":0,\"status\":\"ended\"}\n\n")

		case strings.Contains(r.URL.Path, "/runs/"):
			reason := model.ReasonSuccess
			_ = json.NewEncoder(w).Encode(model.Run{ID: "run-1", TaskName: "alpha", Status: model.PhaseEnded, EndReason: &reason})

		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := apiclient.New(srv.URL, "")

	var code int
	var err error
	out := captureStdout(t, func() {
		code, _, err = followRun(client, "alpha", "run-1", os.Stdout)
	})

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "alpha-line-1", "the run's output must not be silently swallowed")
	assert.Greater(t, streamHits.Load(), int32(emptyStreamsBeforeReady),
		"followRun must retry past the empty (not-yet-persisted) streams")
}
