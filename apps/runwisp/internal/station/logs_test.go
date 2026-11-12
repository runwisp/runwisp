// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/executor"
	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/station/stationtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runLog is a run whose log the test writes line by line, like the executor:
// to disk first, then (optionally) published on the bus.
type runLog struct {
	run    *model.Run
	path   string
	writer *executor.LogWriter
	bus    *events.Bus
}

func newRunLog(t *testing.T, h *harness, executionID string) *runLog {
	t.Helper()
	run := finishedRun(executionID, "job")
	run.Status, run.EndReason, run.EndedAt = model.PhaseRunning, nil, nil
	h.runs.put(run)
	path := logutil.ResolveRunLogPath(h.logDir, run.TaskName, run.ID, run.CreatedAt)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	w, err := executor.NewLogWriter(executor.LogWriterOpts{LogPath: path, LogDir: h.logDir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = w.Close() })
	return &runLog{run: run, path: path, writer: w, bus: h.bus}
}

// write appends lines from..to-1; publish says which of them go on the bus.
func (l *runLog) write(t *testing.T, from, to int64, publish func(n int64) bool) {
	t.Helper()
	for n := from; n < to; n++ {
		text := fmt.Sprintf("line %d", n)
		got, err := l.writer.WriteLineEvent(text, logutil.StreamStdout)
		require.NoError(t, err)
		require.Equal(t, n, got)
		if publish != nil && publish(n) {
			l.bus.Publish(events.EventLogLine, events.LogLineEvent{TaskName: l.run.TaskName, RunID: l.run.ID, ExecutionID: *l.run.ExecutionID, LineNum: n, Text: text, Stream: logutil.StreamStdout})
		}
	}
}

// finish ends the run the way the runtime does: stored, then published.
func (l *runLog) finish(h *harness) {
	ended := finishedRun(*l.run.ExecutionID, l.run.TaskName)
	h.runs.put(ended)
	publishRun(h.bus, events.EventRunCompleted, ended)
}

// collectLines gathers log:lines for subscription id until it holds n lines.
func collectLines(t *testing.T, st *stationtest.Station, id string, n int) []protocol.LogLine {
	t.Helper()
	var lines []protocol.LogLine
	for len(lines) < n {
		frame := st.Wait(t, "log:lines", func(e protocol.Envelope) bool { return e.ReplyTo == id })
		var batch protocol.LogLines
		require.NoError(t, json.Unmarshal(frame.Data, &batch))
		require.Nil(t, batch.Gap, "no gap expected")
		lines = append(lines, batch.Lines...)
	}
	return lines
}

func lineNumbers(lines []protocol.LogLine) []int64 {
	out := make([]int64, len(lines))
	for i, l := range lines {
		out[i] = l.N
	}
	return out
}

func TestLogSubscribe_ReplayThenLiveWithoutGapOrOverlap(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	log := newRunLog(t, h, "e1")
	log.write(t, 0, 5, nil)
	h.start(t)

	id := st.Command(t, "log:subscribe", protocol.LogSubscribe{ExecutionID: "e1", FromLine: 0, Follow: true})
	replayed := collectLines(t, st, id, 5)
	assert.Equal(t, []int64{0, 1, 2, 3, 4}, lineNumbers(replayed))

	// Live: line 5 arrives on the bus; 6-8 never do (dropped from a full
	// buffer, say), so the subscription reads them back from disk when 9
	// shows up.
	log.write(t, 5, 10, func(n int64) bool { return n == 5 || n == 9 })
	live := collectLines(t, st, id, 5)
	assert.Equal(t, []int64{5, 6, 7, 8, 9}, lineNumbers(live))
	assert.Equal(t, "line 7", live[2].Text)

	log.write(t, 10, 12, nil) // written, never published, before the run ends
	log.finish(h)
	tail := collectLines(t, st, id, 2)
	assert.Equal(t, []int64{10, 11}, lineNumbers(tail))

	var res protocol.LogSubscribeResult
	require.True(t, st.Result(t, id, &res).Ok)
	assert.Equal(t, protocol.LogSubscribeResult{NextLine: 12, Eof: true}, res)
}

func TestLogSubscribe_LimitWithoutFollow(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	log := newRunLog(t, h, "e1")
	log.write(t, 0, 6, nil)
	h.start(t)

	id := st.Command(t, "log:subscribe", protocol.LogSubscribe{ExecutionID: "e1", FromLine: 2, Limit: 2})
	assert.Equal(t, []int64{2, 3}, lineNumbers(collectLines(t, st, id, 2)))
	var res protocol.LogSubscribeResult
	require.True(t, st.Result(t, id, &res).Ok)
	assert.Equal(t, protocol.LogSubscribeResult{NextLine: 4}, res)
}

// A negative fromLine asks for a tail: the last -fromLine lines on disk.
func TestLogSubscribe_NegativeFromLineIsATail(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	log := newRunLog(t, h, "e1")
	log.write(t, 0, 6, nil)
	h.start(t)

	id := st.Command(t, "log:subscribe", protocol.LogSubscribe{ExecutionID: "e1", FromLine: -2})
	assert.Equal(t, []int64{4, 5}, lineNumbers(collectLines(t, st, id, 2)))
	var res protocol.LogSubscribeResult
	require.True(t, st.Result(t, id, &res).Ok)
	assert.Equal(t, int64(6), res.NextLine)
}

func TestLogSubscribe_UnsubscribeEndsWithResult(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	log := newRunLog(t, h, "e1")
	log.write(t, 0, 3, nil)
	h.start(t)

	id := st.Command(t, "log:subscribe", protocol.LogSubscribe{ExecutionID: "e1", Follow: true})
	collectLines(t, st, id, 3)
	st.Send(t, "log:unsubscribe", "", protocol.LogUnsubscribe{Subscription: id})
	var res protocol.LogSubscribeResult
	require.True(t, st.Result(t, id, &res).Ok)
	assert.Equal(t, protocol.LogSubscribeResult{NextLine: 3}, res)
}

func TestLogSubscribe_UnknownExecution(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	h.start(t)

	id := st.Command(t, "log:subscribe", protocol.LogSubscribe{ExecutionID: "nope", Follow: true})
	res := st.Result(t, id, nil)
	require.NotNil(t, res.Error)
	assert.Equal(t, protocol.ErrorCodeNotFound, res.Error.Code)
}

func TestLogSearch(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	log := newRunLog(t, h, "e1")
	log.write(t, 0, 20, nil)
	h.start(t)

	id := st.Command(t, "log:search", protocol.LogSearch{ExecutionID: "e1", Query: "line 1", Limit: 3})
	var res protocol.LogSearchResult
	require.True(t, st.Result(t, id, &res).Ok)
	assert.Equal(t, []int64{1, 10, 11}, lineNumbers(res.Hits))
	assert.False(t, res.Exhausted)
	assert.Equal(t, int64(12), res.NextLine)

	id = st.Command(t, "log:search", protocol.LogSearch{ExecutionID: "e1", Query: "(", Regex: true})
	bad := st.Result(t, id, nil)
	require.NotNil(t, bad.Error)
	assert.Equal(t, protocol.ErrorCodeInvalid, bad.Error.Code)
}

// uploadTarget is an upload endpoint that records what it received.
type uploadTarget struct {
	srv *httptest.Server

	mu      sync.Mutex
	method  string
	headers http.Header
	body    string
	puts    int
}

func newUploadTarget(t *testing.T) *uploadTarget {
	u := &uploadTarget{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zr, err := gzip.NewReader(r.Body)
		var body []byte
		if err == nil {
			body, _ = io.ReadAll(zr)
		}
		u.mu.Lock()
		u.method, u.headers, u.body = r.Method, r.Header.Clone(), string(body)
		u.puts++
		u.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

// answerUploads makes st hand out targets on u for every upload:request.
func answerUploads(st *stationtest.Station, u *uploadTarget, archive bool) {
	st.Answer = func(env protocol.Envelope) (any, *protocol.ErrorInfo) {
		if env.Type != "upload:request" {
			return nil, nil
		}
		var req protocol.UploadRequest
		_ = json.Unmarshal(env.Data, &req)
		res := protocol.UploadRequestResult{Uploads: []protocol.UploadTarget{}}
		switch {
		case req.Purpose == protocol.UploadPurposeLogArchive && archive:
			res.Uploads = append(res.Uploads, protocol.UploadTarget{URL: u.srv.URL + "/logs/" + req.ExecutionID, Method: "PUT", Headers: map[string]string{"X-Sig": "abc"}, Key: "logs/" + req.ExecutionID + ".gz"})
		case req.Purpose == protocol.UploadPurposeLogChunk:
			for i := range 2 {
				seq := req.FromSeq + i
				res.Uploads = append(res.Uploads, protocol.UploadTarget{Seq: seq, URL: fmt.Sprintf("%s/chunks/%s/%d", u.srv.URL, req.ExecutionID, seq), Method: "PUT", Key: fmt.Sprintf("chunk-%d", seq)})
			}
		}
		return res, nil
	}
}

func TestUpload_ArchiveAfterFinishSendsSecondUpdate(t *testing.T) {
	st := stationtest.New(t)
	st.AutoAck = true
	target := newUploadTarget(t)
	answerUploads(st, target, true)
	h := newHarness(t, st, nil)
	log := newRunLog(t, h, "e1")
	log.write(t, 0, 2, nil)
	require.NoError(t, h.client.uploader.RegisterDispatch(t.Context(), "e1", false))
	h.start(t)

	log.finish(h)
	first := stationtest.Decode[protocol.ExecutionUpdate](t, st, "execution:update", nil)
	assert.Nil(t, first.Log, "the outcome goes out before the upload")
	req := stationtest.Decode[protocol.UploadRequest](t, st, "upload:request", nil)
	assert.Equal(t, protocol.UploadRequest{ExecutionID: "e1", Purpose: protocol.UploadPurposeLogArchive}, req)
	second := stationtest.Decode[protocol.ExecutionUpdate](t, st, "execution:update", nil)
	require.NotNil(t, second.Log)
	assert.Equal(t, "logs/e1.gz", second.Log.Key)
	assert.Positive(t, second.Log.Bytes)
	assert.Equal(t, protocol.ExecutionStatusFinished, second.Status)
	assert.Equal(t, first.Reason, second.Reason)

	target.mu.Lock()
	assert.Equal(t, "PUT", target.method)
	assert.Equal(t, "abc", target.headers.Get("X-Sig"))
	assert.Empty(t, target.headers.Get("Content-Encoding"), "a target's headers are sent exactly")
	assert.Contains(t, target.body, "line 1")
	target.mu.Unlock()
	assert.NoFileExists(t, log.path, "an archived log is removed locally")
	pending, err := h.db.ListPendingLogUploads(t.Context())
	require.NoError(t, err)
	assert.Empty(t, pending)
}

// An archive that couldn't be uploaded while the daemon was disconnected (or
// down) uploads after the next welcome.
func TestUpload_PendingArchiveRetriedAfterWelcome(t *testing.T) {
	st := stationtest.New(t)
	st.AutoAck = true
	target := newUploadTarget(t)
	answerUploads(st, target, true)
	h := newHarness(t, st, nil)
	log := newRunLog(t, h, "e1")
	log.write(t, 0, 2, nil)
	require.NoError(t, h.client.uploader.RegisterDispatch(t.Context(), "e1", false))
	h.runs.put(finishedRun("e1", "job"))

	// Not connected: the station can't hand out a target, the archive waits.
	assert.Nil(t, h.client.uploader.Archive(t.Context(), finishedRun("e1", "job"), nil))
	assert.FileExists(t, log.path)

	h.client.RecoverArchiveBacklog(t.Context())
	h.start(t)
	stationtest.Decode[protocol.UploadRequest](t, st, "upload:request", nil)
	archived := stationtest.Decode[protocol.ExecutionUpdate](t, st, "execution:update", func(u protocol.ExecutionUpdate) bool { return u.Log != nil })
	assert.Equal(t, "logs/e1.gz", archived.Log.Key)
	assert.NoFileExists(t, log.path)
}

func TestUpload_DeclinedArchiveKeepsTheLog(t *testing.T) {
	st := stationtest.New(t)
	st.AutoAck = true
	target := newUploadTarget(t)
	answerUploads(st, target, false)
	h := newHarness(t, st, nil)
	log := newRunLog(t, h, "e1")
	log.write(t, 0, 2, nil)
	require.NoError(t, h.client.uploader.RegisterDispatch(t.Context(), "e1", false))
	h.start(t)

	log.finish(h)
	stationtest.Decode[protocol.UploadRequest](t, st, "upload:request", nil)
	require.Eventually(t, func() bool {
		pending, err := h.db.ListPendingLogUploads(t.Context())
		return err == nil && len(pending) == 0
	}, stationtest.WaitTimeout, 10*time.Millisecond)
	assert.FileExists(t, log.path)
	assert.Equal(t, 1, st.Count("execution:update"))
	assert.Zero(t, target.puts)
}

// Fewer chunk targets than asked for means the station's cap is reached: the
// stream uploads with what it got, then stops asking.
func TestLogStream_ChunksUseRequestedTargetsUntilCapped(t *testing.T) {
	st := stationtest.New(t)
	target := newUploadTarget(t)
	answerUploads(st, target, false)
	h := newHarness(t, st, nil)
	log := newRunLog(t, h, "e1")
	log.write(t, 0, 3, nil)
	h.start(t)

	s := &chunkStream{executionID: "e1", path: log.path}
	h.client.uploader.flushChunks(t.Context(), s, false)
	req := stationtest.Decode[protocol.UploadRequest](t, st, "upload:request", nil)
	assert.Equal(t, protocol.UploadRequest{ExecutionID: "e1", Purpose: protocol.UploadPurposeLogChunk, FromSeq: 0, Count: logChunkBatch}, req)
	assert.Equal(t, 1, s.seq)
	assert.True(t, s.capped)
	require.Len(t, s.targets, 1, "the second target is kept for the next chunk")

	log.write(t, 3, 4, nil)
	h.client.uploader.flushChunks(t.Context(), s, false)
	assert.Equal(t, 2, s.seq)
	log.write(t, 4, 5, nil)
	h.client.uploader.flushChunks(t.Context(), s, false)
	assert.Equal(t, 2, s.seq, "no targets left after the cap")
	assert.Equal(t, 1, st.Count("upload:request"))
	target.mu.Lock()
	assert.Equal(t, 2, target.puts)
	target.mu.Unlock()
	if s.file != nil {
		_ = s.file.Close()
	}
}
