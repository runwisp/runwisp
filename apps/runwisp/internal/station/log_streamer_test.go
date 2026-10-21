// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
)

// chunkServer records every chunk PUT (path → gunzipped body) and answers
// with the next status in statuses (200 once they run out).
type chunkServer struct {
	*httptest.Server
	mu       sync.Mutex
	puts     []string
	bodies   map[string]string
	statuses []int
}

func newChunkServer(t *testing.T, statuses ...int) *chunkServer {
	t.Helper()
	cs := &chunkServer{bodies: map[string]string{}, statuses: statuses}
	cs.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zr, err := gzip.NewReader(r.Body)
		require.NoError(t, err)
		body, err := io.ReadAll(zr)
		require.NoError(t, err)
		cs.mu.Lock()
		defer cs.mu.Unlock()
		status := http.StatusOK
		if len(cs.statuses) > 0 {
			status, cs.statuses = cs.statuses[0], cs.statuses[1:]
		}
		if status == http.StatusOK {
			cs.puts = append(cs.puts, r.URL.Path)
			cs.bodies[r.URL.Path] = string(body)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(cs.Close)
	return cs
}

func (cs *chunkServer) urls(seqs ...string) []string {
	out := make([]string, len(seqs))
	for i, s := range seqs {
		out[i] = cs.URL + "/" + s
	}
	return out
}

// newTestStream registers a logStream dispatch and starts its stream, with
// every outbound message captured in *sent.
func newTestStream(t *testing.T, cs *chunkServer, logPath string) (*LogUploader, *chunkStream, *[]any) {
	t.Helper()
	u := NewLogUploader(newFakePendingRepo(), &fakeRunRepo{}, t.TempDir(), fixedClock())
	u.httpClient = cs.Client()
	require.NoError(t, u.RegisterDispatch(context.Background(), "exec-1", cs.URL+"/archive", "key", true))
	var sent []any
	var mu sync.Mutex
	u.StartStream(context.Background(), "exec-1", logPath, func(m any) error {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, m)
		return nil
	})
	u.mu.Lock()
	s := u.streams["exec-1"]
	u.mu.Unlock()
	require.NotNil(t, s)
	return u, s, &sent
}

func appendFile(t *testing.T, path, content string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	_, err = f.WriteString(content)
	require.NoError(t, err)
	require.NoError(t, f.Close())
}

func TestLogStream_ShipsWholeLinesAndAsksForURLs(t *testing.T) {
	cs := newChunkServer(t)
	logPath := filepath.Join(t.TempDir(), "run.log")
	appendFile(t, logPath, "a\nb\npart")
	u, s, sent := newTestStream(t, cs, logPath)
	defer u.StopStream(context.Background(), "exec-1")

	require.Len(t, *sent, 1, "starting a stream asks for URLs right away")
	assert.Equal(t, 0, (*sent)[0].(protocol.LogChunkUrlsRequestMessage).FromSeq)

	u.OnChunkUrls(protocol.LogChunkUrlsMessage{ExecutionID: "exec-1", FromSeq: 0, Urls: cs.urls("0", "1")})
	u.flushChunks(context.Background(), s, false)
	appendFile(t, logPath, "ial\nc\n")
	u.flushChunks(context.Background(), s, false)

	assert.Equal(t, []string{"/0", "/1"}, cs.puts)
	assert.Equal(t, "a\nb\n", cs.bodies["/0"], "the unfinished line waits for its newline")
	assert.Equal(t, "partial\nc\n", cs.bodies["/1"])
	assert.Equal(t, 2, s.uploaded())

	appendFile(t, logPath, "d\n")
	u.flushChunks(context.Background(), s, false)
	assert.Len(t, cs.puts, 2, "no URL left, nothing uploaded")
	// Two URLs are below the low-water mark, so the stream asked for more as
	// soon as it used the batch, and that request is still outstanding.
	require.Len(t, *sent, 2)
	assert.Equal(t, 2, (*sent)[1].(protocol.LogChunkUrlsRequestMessage).FromSeq)

	// A stale batch (not continuing at seq 2) is ignored; the right one lands.
	u.OnChunkUrls(protocol.LogChunkUrlsMessage{ExecutionID: "exec-1", FromSeq: 0, Urls: cs.urls("x")})
	u.OnChunkUrls(protocol.LogChunkUrlsMessage{ExecutionID: "exec-1", FromSeq: 2, Urls: cs.urls("2")})
	u.flushChunks(context.Background(), s, false)
	assert.Equal(t, "d\n", cs.bodies["/2"])
}

func TestLogStream_DrainsRotatedSegmentThenFollowsNewFile(t *testing.T) {
	cs := newChunkServer(t)
	logPath := filepath.Join(t.TempDir(), "run.log")
	appendFile(t, logPath, "x1\n")
	u, s, _ := newTestStream(t, cs, logPath)
	defer u.StopStream(context.Background(), "exec-1")
	u.OnChunkUrls(protocol.LogChunkUrlsMessage{ExecutionID: "exec-1", FromSeq: 0, Urls: cs.urls("0", "1", "2")})
	u.flushChunks(context.Background(), s, false)

	// drop_old rotation: the writer finishes the segment, renames it, starts anew.
	appendFile(t, logPath, "x2\n")
	require.NoError(t, os.Rename(logPath, logPath+".prev"))
	appendFile(t, logPath, "y\n")
	u.flushChunks(context.Background(), s, false)

	assert.Equal(t, []string{"/0", "/1", "/2"}, cs.puts)
	assert.Equal(t, "x2\n", cs.bodies["/1"])
	assert.Equal(t, "y\n", cs.bodies["/2"])
}

func TestLogStream_RetriesSameSeqAfterFailure(t *testing.T) {
	cs := newChunkServer(t, http.StatusInternalServerError)
	logPath := filepath.Join(t.TempDir(), "run.log")
	appendFile(t, logPath, "a\n")
	u, s, _ := newTestStream(t, cs, logPath)
	defer u.StopStream(context.Background(), "exec-1")
	u.OnChunkUrls(protocol.LogChunkUrlsMessage{ExecutionID: "exec-1", FromSeq: 0, Urls: cs.urls("0", "1")})

	u.flushChunks(context.Background(), s, false)
	assert.Empty(t, cs.puts)
	appendFile(t, logPath, "b\n")
	u.flushChunks(context.Background(), s, false)
	assert.Equal(t, []string{"/0"}, cs.puts)
	assert.Equal(t, "a\nb\n", cs.bodies["/0"], "the retry ships what accumulated meanwhile")
}

func TestLogStream_RefusedPutStopsStreaming(t *testing.T) {
	cs := newChunkServer(t, http.StatusForbidden)
	logPath := filepath.Join(t.TempDir(), "run.log")
	appendFile(t, logPath, "a\n")
	u, s, sent := newTestStream(t, cs, logPath)
	u.OnChunkUrls(protocol.LogChunkUrlsMessage{ExecutionID: "exec-1", FromSeq: 0, Urls: cs.urls("0", "1")})

	u.flushChunks(context.Background(), s, false)
	u.flushChunks(context.Background(), s, false)
	assert.Empty(t, cs.puts)
	assert.Len(t, *sent, 2, "a capped stream asks for no more URLs after the refusal")
	chunks, _ := u.StopStream(context.Background(), "exec-1")
	assert.Equal(t, 0, chunks)
}

func TestLogStream_StopShipsTailOnlyAfterStreamingStarted(t *testing.T) {
	cs := newChunkServer(t)
	logPath := filepath.Join(t.TempDir(), "run.log")
	appendFile(t, logPath, "short run")
	u, _, _ := newTestStream(t, cs, logPath)
	u.OnChunkUrls(protocol.LogChunkUrlsMessage{ExecutionID: "exec-1", FromSeq: 0, Urls: cs.urls("0")})
	chunks, ok := u.StopStream(context.Background(), "exec-1")
	assert.True(t, ok)
	assert.Equal(t, 0, chunks)
	assert.Empty(t, cs.puts, "a run that never streamed leaves it to the archive")

	logPath = filepath.Join(t.TempDir(), "run.log")
	appendFile(t, logPath, "a\n")
	u, s, _ := newTestStream(t, cs, logPath)
	u.OnChunkUrls(protocol.LogChunkUrlsMessage{ExecutionID: "exec-1", FromSeq: 0, Urls: cs.urls("0", "1")})
	u.flushChunks(context.Background(), s, false)
	appendFile(t, logPath, "no newline")
	chunks, _ = u.StopStream(context.Background(), "exec-1")
	assert.Equal(t, 2, chunks)
	assert.Equal(t, "no newline", cs.bodies["/1"], "the final chunk includes the partial line")
}

func TestLogStream_OnlyForStreamDispatches(t *testing.T) {
	u := NewLogUploader(newFakePendingRepo(), &fakeRunRepo{}, t.TempDir(), fixedClock())
	require.NoError(t, u.RegisterDispatch(context.Background(), "exec-1", "https://upload/x", "key", false))
	u.StartStream(context.Background(), "exec-1", "/nonexistent", func(any) error { return nil })
	assert.Empty(t, u.streams)
	_, ok := u.StopStream(context.Background(), "exec-1")
	assert.False(t, ok)
}
