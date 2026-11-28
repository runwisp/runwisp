// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package station

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/station/stationtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var grepDay = time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)

// grepRunAt stores a finished run started at grepDay plus offset; with lines
// it also writes its log. An empty executionID makes a local run.
func grepRunAt(t *testing.T, h *harness, executionID, task string, offset time.Duration, lines ...string) *model.Run {
	t.Helper()
	started := grepDay.Add(offset)
	run := &model.Run{ID: "run-" + task + "-" + offset.String(), TaskName: task, Status: model.PhaseEnded, CreatedAt: started, StartedAt: &started}
	if executionID != "" {
		run.ExecutionID = new(executionID)
	}
	if lines == nil {
		h.runs.put(run)
		return run
	}
	l := openRunLog(t, h, run)
	for _, text := range lines {
		_, err := l.writer.WriteLineEvent(text, logutil.StreamStdout)
		require.NoError(t, err)
	}
	return run
}

// word is a whole-word, case-folded term.
func word(w string) protocol.LogSearchTerm {
	return protocol.LogSearchTerm{Parts: []string{w}, Start: protocol.TermAnchorWord, End: protocol.TermAnchorWord}
}

// grepResult runs log:search and returns its hits, in order, and its result.
// Every hits frame must fit the welcome's maxFrameBytes.
func grepResult(t *testing.T, st *stationtest.Station, req protocol.LogSearch) ([]protocol.LogSearchHit, protocol.LogSearchResult) {
	t.Helper()
	earlier := st.Count("log:search:hits")
	id := st.Command(t, "log:search", req)
	var res protocol.LogSearchResult
	r := st.Result(t, id, &res)
	require.True(t, r.Ok, "log:search failed: %+v", r.Error)
	// Frames go out in order, so every hits frame is in before the result.
	var hits []protocol.LogSearchHit
	for range st.Count("log:search:hits") - earlier {
		frame := st.Wait(t, "log:search:hits", func(e protocol.Envelope) bool { return e.ReplyTo == id })
		raw, err := json.Marshal(frame)
		require.NoError(t, err)
		require.LessOrEqual(t, len(raw), st.Welcome.MaxFrameBytes)
		var batch protocol.LogSearchHits
		require.NoError(t, json.Unmarshal(frame.Data, &batch))
		require.NotNil(t, batch.Hits)
		hits = append(hits, batch.Hits...)
	}
	return hits, res
}

func hitLines(hits []protocol.LogSearchHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.ExecutionID + ":" + h.Line.Text
	}
	return out
}

func TestLogSearch_FiltersRunsNewestFirstWithContext(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	grepRunAt(t, h, "e1", "station-T1", time.Minute, "boot", "ERROR disk full", "retry", "ok")
	grepRunAt(t, h, "e2", "backup", 2*time.Minute, "error: x", "fine")
	grepRunAt(t, h, "e3", "other", 3*time.Minute, "ERROR other task")
	grepRunAt(t, h, "", "backup", 4*time.Minute, "ERROR local run")
	grepRunAt(t, h, "e4", "backup", -time.Hour, "ERROR before since")
	grepRunAt(t, h, "e5", "backup", 5*time.Minute) // no log on disk
	grepRunAt(t, h, "e6", "backup", time.Hour, "ERROR after until")
	h.start(t)

	hits, res := grepResult(t, st, protocol.LogSearch{
		Terms:     []protocol.LogSearchTerm{word("error")},
		Tasks:     []string{"T1", "backup"},
		Since:     grepDay.UnixMilli(),
		Until:     grepDay.Add(30 * time.Minute).UnixMilli(),
		Context:   1,
		MaxHits:   100,
		TimeoutMs: 5000,
	})
	require.Equal(t, []string{"e2:error: x", "e1:ERROR disk full"}, hitLines(hits))
	assert.Nil(t, hits[0].Before)
	assert.Equal(t, []protocol.LogLine{{N: 1, Stream: protocol.LogStreamStdout, Text: "fine"}}, hits[0].After)
	assert.Equal(t, int64(1), hits[1].Line.N)
	assert.Equal(t, []protocol.LogLine{{N: 0, Stream: protocol.LogStreamStdout, Text: "boot"}}, hits[1].Before)
	assert.Equal(t, []protocol.LogLine{{N: 2, Stream: protocol.LogStreamStdout, Text: "retry"}}, hits[1].After)
	assert.Equal(t, protocol.LogSearchStopDone, res.Stopped)
	// e5 has no log on disk: it counts as scanned, with no bytes.
	assert.Equal(t, 3, res.Progress.RunsTotal)
	assert.Equal(t, 3, res.Progress.RunsScanned)
	assert.Positive(t, res.Progress.BytesScanned)
}

// A service instance's run has no execution id: the station knows it by its
// run id, under its task's name, so a plain task entry covers it. A running
// one is read up to what its log holds now; a local run stays unsearched.
func TestLogSearch_SearchesServiceRuns(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	h.runner.tasks["web"] = &model.Task{Name: "web", Kind: model.KindService, Run: "serve"}
	h.runner.tasks["station-api"] = &model.Task{Name: "station-api", Kind: model.KindService, Run: "serve"}
	old := grepRunAt(t, h, "", "web", 0, "ERROR old instance")
	api := grepRunAt(t, h, "", "station-api", time.Minute, "ERROR api")
	grepRunAt(t, h, "", "backup", 2*time.Minute, "ERROR local run")
	grepRunAt(t, h, "e1", "other", 2*time.Minute, "ERROR other task")
	started := grepDay.Add(3 * time.Minute)
	live := openRunLog(t, h, &model.Run{ID: "run-web-live", TaskName: "web", Status: model.PhaseRunning, CreatedAt: started, StartedAt: &started})
	h.start(t)
	for _, text := range []string{"boot", "ERROR live instance"} {
		_, err := live.writer.WriteLineEvent(text, logutil.StreamStdout)
		require.NoError(t, err)
	}

	hits, res := grepResult(t, st, protocol.LogSearch{
		Terms: []protocol.LogSearchTerm{word("error")}, Tasks: []string{"web", "api", "backup"}, MaxHits: 100, TimeoutMs: 5000,
	})
	assert.Equal(t, []string{"run-web-live:ERROR live instance", api.ID + ":ERROR api", old.ID + ":ERROR old instance"}, hitLines(hits))
	assert.Equal(t, 3, res.Progress.RunsTotal)
}

func TestLogSearch_MaxHitsKeepsTheLastHitsContext(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	grepRunAt(t, h, "e1", "job", 0, "hit 0", "hit 1", "hit 2", "hit 3", "hit 4", "hit 5")
	grepRunAt(t, h, "e0", "job", -time.Minute, "hit older run")
	h.start(t)

	hits, res := grepResult(t, st, protocol.LogSearch{Terms: []protocol.LogSearchTerm{word("hit")}, Context: 2, MaxHits: 3, TimeoutMs: 5000})
	require.Equal(t, []string{"e1:hit 0", "e1:hit 1", "e1:hit 2"}, hitLines(hits))
	assert.Equal(t, []string{"hit 3", "hit 4"}, []string{hits[2].After[0].Text, hits[2].After[1].Text})
	assert.Equal(t, protocol.LogSearchStopMaxHits, res.Stopped)
	assert.Equal(t, 1, res.Progress.RunsScanned, "a cap ends the scan")
}

func TestLogSearch_MaxBytes(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	grepRunAt(t, h, "e1", "job", 0, "hit 0", "hit 1", "hit 2", "hit 3")
	h.start(t)

	hits, res := grepResult(t, st, protocol.LogSearch{Terms: []protocol.LogSearchTerm{word("hit")}, MaxHits: 100, MaxBytes: 12, TimeoutMs: 5000})
	assert.Len(t, hits, 3, "5 + 5 + 5 bytes reach 12")
	assert.Equal(t, protocol.LogSearchStopMaxBytes, res.Stopped)
}

// Long lines are cut to 4 KiB, and a hit whose context would overflow the
// frame loses context lines instead.
func TestLogSearch_FramesStayUnderMaxFrameBytes(t *testing.T) {
	st := stationtest.New(t)
	st.Welcome.MaxFrameBytes = minMaxFrameBytes
	h := newHarness(t, st, nil)
	long := "needle " + strings.Repeat("<", 10000) // each < encodes as 6 bytes
	grepRunAt(t, h, "e1", "job", 0, long, long, long, long)
	h.start(t)

	hits, _ := grepResult(t, st, protocol.LogSearch{Terms: []protocol.LogSearchTerm{word("needle")}, Context: 5, MaxHits: 100, TimeoutMs: 5000})
	require.Len(t, hits, 4)
	for _, hit := range hits {
		assert.Len(t, hit.Line.Text, grepMaxLineBytes)
		assert.True(t, hit.Line.Continued)
		assert.Less(t, len(hit.Before)+len(hit.After), 3, "context trimmed to fit")
	}
}

func TestLogSearch_InvalidTerms(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	h.start(t)

	empty := word("")
	unknownAnchor := word("error")
	unknownAnchor.End = "line"
	for _, req := range []protocol.LogSearch{
		{Terms: []protocol.LogSearchTerm{empty}},
		{Terms: []protocol.LogSearchTerm{unknownAnchor}},
		{Terms: []protocol.LogSearchTerm{word("error")}, Regex: "err"}, // both
		{}, // neither
	} {
		req.MaxHits, req.TimeoutMs = 1, 1000
		id := st.Command(t, "log:search", req)
		res := st.Result(t, id, nil)
		require.NotNil(t, res.Error, "%+v", req)
		assert.Equal(t, protocol.ErrorCodeInvalid, res.Error.Code, "%+v", req)
	}
}

// endlessRun stores a run whose log is a FIFO fed lines until the reader goes
// away, so a grep over it ends only when something stops it.
func endlessRun(t *testing.T, h *harness) {
	t.Helper()
	run := grepRunAt(t, h, "e1", "job", 0)
	path := logutil.ResolveRunLogPath(h.logDir, run.TaskName, run.ID, run.CreatedAt)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, syscall.Mkfifo(path, 0o600))
	go func() {
		f, err := os.OpenFile(path, os.O_WRONLY, 0) // blocks until the grep opens it
		if err != nil {
			return
		}
		defer f.Close()
		for {
			if _, err := f.WriteString("nothing to see here\n"); err != nil {
				return
			}
		}
	}()
}

func TestLogSearch_CancelStopsTheScan(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	endlessRun(t, h)
	h.start(t)

	id := st.Command(t, "log:search", protocol.LogSearch{Terms: []protocol.LogSearchTerm{word("absent")}, MaxHits: 10, TimeoutMs: 60000})
	// Progress frames while it scans: the grep is past registering itself.
	st.Wait(t, "log:search:hits", func(e protocol.Envelope) bool { return e.ReplyTo == id })
	st.Send(t, "cancel", "", protocol.Cancel{Command: id})
	var res protocol.LogSearchResult
	require.True(t, st.Result(t, id, &res).Ok)
	assert.Equal(t, protocol.LogSearchStopCancelled, res.Stopped)
	assert.Equal(t, 1, res.Progress.RunsTotal)
	assert.Positive(t, res.Progress.BytesScanned)
}

func TestLogSearch_TimeoutStopsTheScan(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	endlessRun(t, h)
	h.start(t)

	id := st.Command(t, "log:search", protocol.LogSearch{Terms: []protocol.LogSearchTerm{word("absent")}, MaxHits: 10, TimeoutMs: 100})
	var res protocol.LogSearchResult
	require.True(t, st.Result(t, id, &res).Ok)
	assert.Equal(t, protocol.LogSearchStopTimeout, res.Stopped)
}

func TestGrepRing_KeepsTheLastLinesInOrder(t *testing.T) {
	r := grepRing{ring: make([]ringLine, 3)}
	assert.Nil(t, r.lines())
	long := strings.Repeat("é", grepMaxLineBytes) // 2 bytes a rune
	for n := range int64(6) {
		text := fmt.Sprint("line ", n)
		if n == 4 {
			text = long
		}
		r.push(n, logutil.StreamStdout, []byte(text))
	}
	lines := r.lines()
	require.Len(t, lines, 3)
	assert.Equal(t, []int64{3, 4, 5}, []int64{lines[0].N, lines[1].N, lines[2].N})
	assert.Equal(t, "line 3", lines[0].Text)
	assert.Equal(t, long[:grepMaxLineBytes], lines[1].Text, "cut as a hit line is")
	assert.True(t, lines[1].Continued)
	assert.Equal(t, "line 5", lines[2].Text)

	r.reset()
	assert.Nil(t, r.lines(), "a new run starts without context")
	assert.Nil(t, (&grepRing{}).lines(), "context 0 keeps nothing")
}

func TestLogSearch_BusyPastTheConcurrencyLimit(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	endlessRun(t, h)
	h.start(t)

	req := protocol.LogSearch{Terms: []protocol.LogSearchTerm{word("absent")}, MaxHits: 10, TimeoutMs: 60000}
	var running []string
	for range grepConcurrency {
		id := st.Command(t, "log:search", req)
		st.Wait(t, "log:search:hits", func(e protocol.Envelope) bool { return e.ReplyTo == id })
		running = append(running, id)
	}
	res := st.Result(t, st.Command(t, "log:search", req), nil)
	require.NotNil(t, res.Error)
	assert.Equal(t, protocol.ErrorCodeBusy, res.Error.Code)
	assert.True(t, res.Error.Retryable)

	st.Send(t, "cancel", "", protocol.Cancel{Command: running[0]})
	require.True(t, st.Result(t, running[0], nil).Ok)
	id := st.Command(t, "log:search", req)
	st.Wait(t, "log:search:hits", func(e protocol.Envelope) bool { return e.ReplyTo == id })
	for _, id := range append(running[1:], id) {
		st.Send(t, "cancel", "", protocol.Cancel{Command: id})
		require.True(t, st.Result(t, id, nil).Ok)
	}
}

// Hits from many runs share frames: a frame goes out every 250 ms, or when
// the next hit would not fit.
func TestLogSearch_BatchesHitsAcrossRuns(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	for i := range 50 {
		grepRunAt(t, h, fmt.Sprint("e", i), "job", time.Duration(i)*time.Second, "a hit")
	}
	h.start(t)

	hits, res := grepResult(t, st, protocol.LogSearch{Terms: []protocol.LogSearchTerm{word("hit")}, MaxHits: 100, TimeoutMs: 5000})
	require.Len(t, hits, 50)
	assert.Equal(t, protocol.LogSearchStopDone, res.Stopped)
	assert.Less(t, st.Count("log:search:hits"), 5, "not a frame per run")
}
