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
func word(w string) protocol.LogGrepTerm {
	return protocol.LogGrepTerm{Parts: []string{w}, WordStart: true, WordEnd: true}
}

// grepResult runs log:grep and returns its hits, in order, and its result.
// Every hits frame must fit the welcome's maxFrameBytes.
func grepResult(t *testing.T, st *stationtest.Station, req protocol.LogGrep) ([]protocol.LogGrepHit, protocol.LogGrepResult) {
	t.Helper()
	id := st.Command(t, "log:grep", req, "log.grep")
	var res protocol.LogGrepResult
	r := st.Result(t, id, &res)
	require.True(t, r.Ok, "log:grep failed: %+v", r.Error)
	// Frames go out in order, so every hits frame is in before the result.
	var hits []protocol.LogGrepHit
	for range st.Count("log:grep:hits") {
		frame := st.Wait(t, "log:grep:hits", func(e protocol.Envelope) bool { return e.ReplyTo == id })
		raw, err := json.Marshal(frame)
		require.NoError(t, err)
		require.LessOrEqual(t, len(raw), st.Welcome.MaxFrameBytes)
		var batch protocol.LogGrepHits
		require.NoError(t, json.Unmarshal(frame.Data, &batch))
		require.NotNil(t, batch.Hits)
		hits = append(hits, batch.Hits...)
	}
	return hits, res
}

func hitLines(hits []protocol.LogGrepHit) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.ExecutionID + ":" + h.Line.Text
	}
	return out
}

func TestLogGrep_FiltersRunsNewestFirstWithContext(t *testing.T) {
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

	hits, res := grepResult(t, st, protocol.LogGrep{
		Terms:     []protocol.LogGrepTerm{word("error")},
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
	assert.Equal(t, protocol.StoppedDone, res.Stopped)
	assert.Equal(t, 2, res.Hits)
	// e5 has no log on disk: it counts as scanned, with no bytes.
	assert.Equal(t, 3, res.Progress.RunsTotal)
	assert.Equal(t, 3, res.Progress.RunsScanned)
	assert.Positive(t, res.Progress.BytesScanned)
}

func TestLogGrep_MaxHitsKeepsTheLastHitsContext(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	grepRunAt(t, h, "e1", "job", 0, "hit 0", "hit 1", "hit 2", "hit 3", "hit 4", "hit 5")
	grepRunAt(t, h, "e0", "job", -time.Minute, "hit older run")
	h.start(t)

	hits, res := grepResult(t, st, protocol.LogGrep{Terms: []protocol.LogGrepTerm{word("hit")}, Context: 2, MaxHits: 3, TimeoutMs: 5000})
	require.Equal(t, []string{"e1:hit 0", "e1:hit 1", "e1:hit 2"}, hitLines(hits))
	assert.Equal(t, []string{"hit 3", "hit 4"}, []string{hits[2].After[0].Text, hits[2].After[1].Text})
	assert.Equal(t, protocol.StoppedMaxHits, res.Stopped)
	assert.Equal(t, 3, res.Hits)
	assert.Equal(t, 1, res.Progress.RunsScanned, "a cap ends the scan")
}

func TestLogGrep_MaxBytes(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	grepRunAt(t, h, "e1", "job", 0, "hit 0", "hit 1", "hit 2", "hit 3")
	h.start(t)

	hits, res := grepResult(t, st, protocol.LogGrep{Terms: []protocol.LogGrepTerm{word("hit")}, MaxHits: 100, MaxBytes: 12, TimeoutMs: 5000})
	assert.Len(t, hits, 3, "5 + 5 + 5 bytes reach 12")
	assert.Equal(t, protocol.StoppedMaxBytes, res.Stopped)
	assert.Equal(t, 3, res.Hits)
}

// Long lines are cut to 4 KiB, and a hit whose context would overflow the
// frame loses context lines instead.
func TestLogGrep_FramesStayUnderMaxFrameBytes(t *testing.T) {
	st := stationtest.New(t)
	st.Welcome.MaxFrameBytes = minMaxFrameBytes
	h := newHarness(t, st, nil)
	long := "needle " + strings.Repeat("<", 10000) // each < encodes as 6 bytes
	grepRunAt(t, h, "e1", "job", 0, long, long, long, long)
	h.start(t)

	hits, res := grepResult(t, st, protocol.LogGrep{Terms: []protocol.LogGrepTerm{word("needle")}, Context: 5, MaxHits: 100, TimeoutMs: 5000})
	require.Len(t, hits, 4)
	assert.Equal(t, 4, res.Hits)
	for _, hit := range hits {
		assert.Len(t, hit.Line.Text, grepMaxLineBytes)
		assert.True(t, hit.Line.Continued)
		assert.Less(t, len(hit.Before)+len(hit.After), 3, "context trimmed to fit")
	}
}

func TestLogGrep_InvalidTerms(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	h.start(t)

	id := st.Command(t, "log:grep", protocol.LogGrep{Terms: []protocol.LogGrepTerm{{Parts: []string{""}}}, MaxHits: 1, TimeoutMs: 1000}, "log.grep")
	res := st.Result(t, id, nil)
	require.NotNil(t, res.Error)
	assert.Equal(t, protocol.ErrorCodeInvalid, res.Error.Code)
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

func TestLogGrep_CancelStopsTheScan(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	endlessRun(t, h)
	h.start(t)

	id := st.Command(t, "log:grep", protocol.LogGrep{Terms: []protocol.LogGrepTerm{word("absent")}, MaxHits: 10, TimeoutMs: 60000}, "log.grep")
	// Progress frames while it scans: the grep is past registering itself.
	st.Wait(t, "log:grep:hits", func(e protocol.Envelope) bool { return e.ReplyTo == id })
	st.Send(t, "log:grep:cancel", "", protocol.LogGrepCancel{Search: id})
	var res protocol.LogGrepResult
	require.True(t, st.Result(t, id, &res).Ok)
	assert.Equal(t, protocol.StoppedCancelled, res.Stopped)
	assert.Equal(t, 1, res.Progress.RunsTotal)
	assert.Positive(t, res.Progress.BytesScanned)
}

func TestLogGrep_TimeoutStopsTheScan(t *testing.T) {
	st := stationtest.New(t)
	h := newHarness(t, st, nil)
	endlessRun(t, h)
	h.start(t)

	id := st.Command(t, "log:grep", protocol.LogGrep{Terms: []protocol.LogGrepTerm{word("absent")}, MaxHits: 10, TimeoutMs: 100}, "log.grep")
	var res protocol.LogGrepResult
	require.True(t, st.Result(t, id, &res).Ok)
	assert.Equal(t, protocol.StoppedTimeout, res.Stopped)
	assert.Zero(t, res.Hits)
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
