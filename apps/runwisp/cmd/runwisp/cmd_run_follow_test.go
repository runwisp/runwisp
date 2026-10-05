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
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFinishExecJSON_ReusesFetchedRunWithoutRefetch: exec --json must report
// the exit code already captured during the follow, not do a second GetRun that could fail and mask a real success as a
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
	err := finishExecJSON(t.Context(), &buf, client, "alpha", "run-1", final)
	require.NoError(t, err, "a terminal run already in hand must be reused, never re-fetched")

	var doc runJSONDoc
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

// The daemon answers 404 (not an empty 200) for a log stream whose run row has
// not landed yet; followRun must treat that like the empty stream and retry
// rather than abort with an open error.
func TestFollowRun_RetriesNotFoundUntilRunIsStreamable(t *testing.T) {
	var streamHits atomic.Int32
	const notFoundBeforeReady = 2

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/log/stream"):
			if int(streamHits.Add(1)) <= notFoundBeforeReady {
				http.Error(w, "not found", http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
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
	assert.Contains(t, out, "alpha-line-1")
	assert.Greater(t, streamHits.Load(), int32(notFoundBeforeReady))
}

// A refused stream (503 past the cap) is not retried as if the run were
// pending: the open error reaches the caller.
func TestFollowRun_StreamRefusalSurfaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "too many streams", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	client := apiclient.New(srv.URL, "")
	var err error
	captureStdout(t, func() {
		_, _, err = followRun(client, "alpha", "run-1", os.Stdout)
	})

	var status *apiclient.HTTPStatusError
	require.ErrorAs(t, err, &status)
	assert.Equal(t, http.StatusServiceUnavailable, status.StatusCode)
}

// TestFollowRun_RereadsNonTerminalRowAfterDone is the regression test for the
// exec --json stale-status bug seen from the client side: the stream says done
// while the run row still reads "running". A non-terminal row means a write that
// has not landed yet, never the truth — trusting it yields a nil end_reason,
// which reads as success, so a run that failed with exit 7 would exit 0.
// followRun must re-read until the terminal row is visible.
func TestFollowRun_RereadsNonTerminalRowAfterDone(t *testing.T) {
	var runHits atomic.Int32
	const staleReadsBeforeTerminal = 2

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/log/stream"):
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "event: done\ndata: {\"final_line\":-1,\"status\":\"ended\"}\n\n")

		case strings.Contains(r.URL.Path, "/runs/"):
			run := model.Run{ID: "run-1", TaskName: "alpha", Status: model.PhaseRunning}
			if runHits.Add(1) > staleReadsBeforeTerminal {
				reason := model.ReasonFailed
				run.Status = model.PhaseEnded
				run.EndReason = &reason
				run.ExitCode = 7
			}
			_ = json.NewEncoder(w).Encode(run)

		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	code, final, err := followRun(apiclient.New(srv.URL, ""), "alpha", "run-1", os.Stdout)
	require.NoError(t, err)
	require.NotNil(t, final)
	assert.Equal(t, model.PhaseEnded, final.Status, "a non-terminal row after done must be re-read")
	assert.Equal(t, 7, code, "a failed run must not be reported as exit 0")
}

// TestFollowRunLog_QuietStreamDoesNotSpendStallBudget is the regression test
// for giving up on a quiet run: the server closes every log stream after
// LogStreamTimeout, so a run that prints nothing for that long yields an empty
// stream too. Counting those as stalls made `runwisp run` abandon a run silent
// for followMaxStalls × LogStreamTimeout (~8h), read the still-running row, and
// exit 0. A stream that stayed open past quietAfter was live and must just be
// re-opened.
func TestFollowRunLog_QuietStreamDoesNotSpendStallBudget(t *testing.T) {
	const quietAfter = 5 * time.Millisecond
	var streamHits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if streamHits.Add(1) <= followMaxStalls+5 {
			w.(http.Flusher).Flush()
			time.Sleep(2 * quietAfter) // a live connection with nothing to say
			return
		}
		fmt.Fprint(w, "event: line\ndata: {\"n\":0,\"stream\":\"stdout\",\"text\":\"finally\"}\n\n")
		fmt.Fprint(w, "event: done\ndata: {\"finalLine\":0,\"status\":\"ended\"}\n\n")
	}))
	defer srv.Close()

	var got []string
	done, err := followRunLog(t.Context(), apiclient.New(srv.URL, ""), "task", "run-1", 0, quietAfter, func(l server.LogLineEntry) {
		got = append(got, l.Text)
	})
	require.NoError(t, err)
	assert.True(t, done, "a quiet run must be followed until it ends")
	assert.Equal(t, []string{"finally"}, got)
}
