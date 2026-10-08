// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/server"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// syncBuffer is a bytes.Buffer safe to read while a follow is still writing.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fakeLogDaemon serves just enough of the daemon's API for `runwisp logs`: the
// task list, run listing/lookup, paged logs with rotation, and log/event SSE
// streams. Streams replay the run's lines and send done when the run ended;
// the event stream sends the queued events and then stays open.
type fakeLogDaemon struct {
	mu             sync.Mutex
	tasks          []model.TaskResponse
	runs           []model.Run // newest first, like the real listing
	lines          map[string][]server.LogLineEntry
	firstAvailable map[string]int64
	unlisted       map[string]bool // runs lookups find but listings don't (yet)
	events         []string        // raw "event: ...\ndata: ...\n\n" frames
	eventHits      int
	// control answers POST /api/tasks/{name}/{action} under mu with an HTTP
	// status; it may add runs and events.
	control func(action, name string) int
}

func newFakeLogDaemon() *fakeLogDaemon {
	return &fakeLogDaemon{lines: map[string][]server.LogLineEntry{}, firstAvailable: map[string]int64{}, unlisted: map[string]bool{}}
}

func (d *fakeLogDaemon) addTask(name string, kind model.TaskKind, instances int) {
	d.tasks = append(d.tasks, model.TaskResponse{Task: model.Task{Name: name, Kind: kind, Instances: instances}})
}

// addRun records a run with the given output; lines prefixed "err:" are
// stderr. A reason of "" leaves the run running.
func (d *fakeLogDaemon) addRun(id, task string, instance int, reason model.EndReason, exit int, output ...string) model.Run {
	now := time.Unix(1700000000, 0)
	run := model.Run{ID: id, TaskName: task, Status: model.PhaseRunning, InstanceIndex: instance, StartedAt: &now, ExitCode: exit}
	if reason != "" {
		run.Status = model.PhaseEnded
		run.EndReason = &reason
		run.IsFailure = (&model.Task{}).IsFailureReason(reason, exit, false)
		end := now.Add(time.Second)
		run.EndedAt = &end
	}
	for i, text := range output {
		stream := "stdout"
		if rest, ok := strings.CutPrefix(text, "err:"); ok {
			stream, text = "stderr", rest
		}
		d.lines[id] = append(d.lines[id], server.LogLineEntry{N: int64(i), Stream: stream, Text: text})
	}
	d.runs = append([]model.Run{run}, d.runs...)
	return run
}

// end finishes a running run with reason, as a stop would.
func (d *fakeLogDaemon) end(id string, reason model.EndReason) model.Run {
	for i := range d.runs {
		if d.runs[i].ID == id {
			d.runs[i].Status = model.PhaseEnded
			d.runs[i].EndReason = &reason
			d.runs[i].IsFailure = (&model.Task{}).IsFailureReason(reason, d.runs[i].ExitCode, false)
			return d.runs[i]
		}
	}
	panic("no run " + id)
}

func (d *fakeLogDaemon) run(id string) (model.Run, bool) {
	for _, r := range d.runs {
		if r.ID == id {
			return r, true
		}
	}
	return model.Run{}, false
}

// page mirrors logutil.ReadLineRange: negative from counts from the end, and
// nothing below firstAvailable is readable.
func (d *fakeLogDaemon) page(id string, from, limit int64) server.LogPageBody {
	all := d.lines[id]
	total, first := int64(len(all)), d.firstAvailable[id]
	if from < 0 {
		from = total + from
	}
	from = max(from, first)
	to := min(from+limit, total)
	body := server.LogPageBody{Lines: []server.LogLineEntry{}, FirstAvailable: first, TotalLines: total, Truncated: first > 0}
	if from < to {
		body.Lines = all[from:to]
	}
	return body
}

func (d *fakeLogDaemon) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /api/tasks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(server.TasksResponseBody{Items: d.tasks})
	})
	mux.HandleFunc("GET /api/runs", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		items := []model.Run{}
		for _, run := range d.runs {
			if !d.unlisted[run.ID] && (q.Get("status") == "" || string(run.Status) == q.Get("status")) &&
				(q.Get("taskName") == "" || run.TaskName == q.Get("taskName")) &&
				(limit == 0 || len(items) < limit) {
				items = append(items, run)
			}
		}
		_ = json.NewEncoder(w).Encode(server.RunsResponseBody{Items: items, Total: int64(len(items))})
	})
	mux.HandleFunc("GET /api/runs/{id}", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		run, ok := d.run(r.PathValue("id"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(run)
	})
	mux.HandleFunc("GET /api/runs/{id}/log", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		from, _ := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64)
		limit, _ := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64)
		_ = json.NewEncoder(w).Encode(d.page(r.PathValue("id"), from, limit))
	})
	mux.HandleFunc("GET /api/runs/{id}/log/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		from, _ := strconv.ParseInt(r.URL.Query().Get("from"), 10, 64)
		d.mu.Lock()
		id := r.PathValue("id")
		page := d.page(id, from, 1<<20)
		run, _ := d.run(id)
		d.mu.Unlock()
		for _, l := range page.Lines {
			b, _ := json.Marshal(l)
			fmt.Fprintf(w, "event: line\ndata: %s\n\n", b)
		}
		if run.Status == model.PhaseEnded {
			fmt.Fprint(w, "event: done\ndata: {\"finalLine\":0,\"status\":\"ended\"}\n\n")
			return
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	mux.HandleFunc("GET /api/events/stream", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		d.mu.Lock()
		d.eventHits++
		d.mu.Unlock()
		fmt.Fprint(w, "event: ping\ndata: {}\n\n")
		for sent := 0; ; {
			d.mu.Lock()
			frames := d.events[sent:]
			sent = len(d.events)
			d.mu.Unlock()
			for _, f := range frames {
				fmt.Fprint(w, f)
			}
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(5 * time.Millisecond):
			}
		}
	})
	mux.HandleFunc("POST /api/tasks/{name}/{action}", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		w.WriteHeader(d.control(r.PathValue("action"), r.PathValue("name")))
	})
	return mux
}

// runEventFrame is an app-stream frame announcing run.
func runEventFrame(eventType string, run model.Run) string {
	b, _ := json.Marshal(server.RunEventBody{Run: &run})
	return fmt.Sprintf("event: %s\nid: 1\ndata: %s\n\n", eventType, b)
}

// runLogsAgainst runs `runwisp logs` against d with the given -f/-n/--json.
func runLogsAgainst(t *testing.T, ctx context.Context, d *fakeLogDaemon, out, errOut *syncBuffer, follow bool, lines string, jsonOut bool, args ...string) error {
	t.Helper()
	f, _, _ := serveServiceSocket(t, d.handler())
	opts, err := parseLogsOptions(follow, lines, jsonOut)
	require.NoError(t, err)
	return runLogs(ctx, out, errOut, f, remoteFlags{}, args, opts)
}

// captureCLISlog points the default logger at a buffer for the test, where the
// CLI's notices (run outcomes, rotation warnings…) land.
func captureCLISlog(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

func TestParseLogsOptions(t *testing.T) {
	for _, tc := range []struct {
		lines  string
		follow bool
		want   logsOptions
		err    string
	}{
		{lines: "", want: logsOptions{Lines: -1}},
		{lines: "", follow: true, want: logsOptions{Follow: true, Lines: followTailLines}},
		{lines: "20", want: logsOptions{Lines: 20}},
		{lines: "0", follow: true, want: logsOptions{Follow: true, Lines: 0}},
		{lines: "+20", want: logsOptions{Head: true, Lines: 20}},
		{lines: "+5", follow: true, err: "can't be combined with --follow"},
		{lines: "-5", err: "invalid --lines"},
		{lines: "++5", err: "invalid --lines"},
		{lines: "+-5", err: "invalid --lines"},
		{lines: "all", err: "invalid --lines"},
	} {
		t.Run(fmt.Sprintf("%q follow=%v", tc.lines, tc.follow), func(t *testing.T) {
			got, err := parseLogsOptions(tc.follow, tc.lines, false)
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// A single task prints its last run's output untouched, keeping each line's
// stream, so `runwisp logs backup | grep` sees exactly what the task printed.
func TestLogs_SingleTaskIsRawAndKeepsStreams(t *testing.T) {
	slogBuf := captureCLISlog(t)
	d := newFakeLogDaemon()
	d.addTask("backup", model.KindTask, 0)
	d.addRun("01J00000000000000000000001", "backup", 0, model.ReasonSuccess, 0, "old run")
	d.addRun("01J00000000000000000000002", "backup", 0, model.ReasonFailed, 7, "copying", "err:disk full")

	var out, errOut syncBuffer
	require.NoError(t, runLogsAgainst(t, t.Context(), d, &out, &errOut, false, "", false, "backup"))

	assert.Equal(t, "copying\n", out.String(), "only the most recent run, with no prefix")
	assert.Equal(t, "disk full\n", errOut.String(), "the task's stderr stays on stderr")
	assert.Contains(t, slogBuf.String(), `msg="run failed"`, "how the run ended is reported")
	assert.Contains(t, slogBuf.String(), "exit=7")
}

// An active run wins over the last finished one, and a still-running run
// prints its output so far without an outcome.
func TestLogs_PrefersActiveRuns(t *testing.T) {
	slogBuf := captureCLISlog(t)
	d := newFakeLogDaemon()
	d.addTask("backup", model.KindTask, 0)
	d.addRun("01J00000000000000000000001", "backup", 0, model.ReasonSuccess, 0, "finished")
	d.addRun("01J00000000000000000000002", "backup", 0, "", 0, "in progress")

	var out, errOut syncBuffer
	require.NoError(t, runLogsAgainst(t, t.Context(), d, &out, &errOut, false, "", false, "backup"))
	assert.Equal(t, "in progress\n", out.String())
	assert.NotContains(t, slogBuf.String(), "run succeeded")
}

// Several targets, or a service with several instances, prefix each line with
// its source, padded to one width. A glob also matches manual_trigger = false
// entries: reading a log isn't control.
func TestLogs_PrefixesMixedSources(t *testing.T) {
	captureCLISlog(t)
	d := newFakeLogDaemon()
	d.addTask("web", model.KindService, 2)
	d.tasks = append(d.tasks, model.TaskResponse{Task: model.Task{Name: "db", Kind: model.KindTask, ManualTrigger: false}})
	d.addRun("01J00000000000000000000001", "db", 0, model.ReasonSuccess, 0, "vacuumed")
	d.addRun("01J00000000000000000000002", "web", 0, "", 0, "listening a")
	d.addRun("01J00000000000000000000003", "web", 1, "", 0, "listening b")

	var out, errOut syncBuffer
	require.NoError(t, runLogsAgainst(t, t.Context(), d, &out, &errOut, false, "", false, "*"))
	assert.Equal(t, "web#1 | listening a\nweb#2 | listening b\ndb    | vacuumed\n", out.String())

	t.Run("a multi-instance service alone is prefixed", func(t *testing.T) {
		var out, errOut syncBuffer
		require.NoError(t, runLogsAgainst(t, t.Context(), d, &out, &errOut, false, "", false, "web"))
		assert.Equal(t, "web#1 | listening a\nweb#2 | listening b\n", out.String())
	})
}

func TestLogs_TailHeadAndPaging(t *testing.T) {
	captureCLISlog(t)
	d := newFakeLogDaemon()
	d.addTask("big", model.KindTask, 0)
	const total = server.LogPageMaxLimit + 2500
	output := make([]string, total)
	for i := range output {
		output[i] = "line " + strconv.Itoa(i)
	}
	d.addRun("01J00000000000000000000001", "big", 0, model.ReasonSuccess, 0, output...)

	lines := func(t *testing.T, n string) []string {
		var out, errOut syncBuffer
		require.NoError(t, runLogsAgainst(t, t.Context(), d, &out, &errOut, false, n, false, "big"))
		return strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	}

	t.Run("all lines across pages", func(t *testing.T) {
		got := lines(t, "")
		require.Len(t, got, total)
		assert.Equal(t, "line 0", got[0])
		assert.Equal(t, "line "+strconv.Itoa(total-1), got[total-1])
	})
	t.Run("tail", func(t *testing.T) {
		assert.Equal(t, []string{"line " + strconv.Itoa(total-2), "line " + strconv.Itoa(total-1)}, lines(t, "2"))
	})
	t.Run("tail longer than a page", func(t *testing.T) {
		got := lines(t, strconv.Itoa(server.LogPageMaxLimit+10))
		require.Len(t, got, server.LogPageMaxLimit+10)
		assert.Equal(t, "line "+strconv.Itoa(total-1), got[len(got)-1])
	})
	t.Run("head", func(t *testing.T) {
		assert.Equal(t, []string{"line 0", "line 1", "line 2"}, lines(t, "+3"))
	})
}

// Lines rotated away by log_max_size are called out rather than silently missing.
func TestLogs_RotatedLinesAreReported(t *testing.T) {
	slogBuf := captureCLISlog(t)
	d := newFakeLogDaemon()
	d.addTask("big", model.KindTask, 0)
	d.addRun("01J00000000000000000000001", "big", 0, model.ReasonSuccess, 0, "a", "b", "c", "d")
	d.firstAvailable["01J00000000000000000000001"] = 2

	var out, errOut syncBuffer
	require.NoError(t, runLogsAgainst(t, t.Context(), d, &out, &errOut, false, "+1", false, "big"))
	assert.Equal(t, "c\n", out.String(), "the head starts at the first line still on disk")
	assert.Contains(t, slogBuf.String(), "rotated away")

	t.Run("a tail inside the kept window says nothing", func(t *testing.T) {
		slogBuf := captureCLISlog(t)
		var out, errOut syncBuffer
		require.NoError(t, runLogsAgainst(t, t.Context(), d, &out, &errOut, false, "2", false, "big"))
		assert.Equal(t, "c\nd\n", out.String())
		assert.NotContains(t, slogBuf.String(), "rotated away")
	})
}

func TestLogs_JSONRecords(t *testing.T) {
	captureCLISlog(t)
	d := newFakeLogDaemon()
	d.addTask("backup", model.KindTask, 0)
	d.addRun("01J00000000000000000000001", "backup", 0, model.ReasonFailed, 7, "copying", "err:disk full <sda>")

	var out, errOut syncBuffer
	require.NoError(t, runLogsAgainst(t, t.Context(), d, &out, &errOut, false, "", true, "backup"))
	assert.Empty(t, errOut.String(), "--json keeps every record on stdout")

	recs := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	require.Len(t, recs, 3)
	assert.JSONEq(t, `{"type":"line","task":"backup","runId":"01J00000000000000000000001","n":0,"ts":0,"stream":"stdout","text":"copying"}`, recs[0])
	assert.Contains(t, recs[1], `"text":"disk full <sda>"`, "log text is not HTML-escaped")

	var end map[string]any
	require.NoError(t, json.Unmarshal([]byte(recs[2]), &end))
	assert.Equal(t, "end", end["type"])
	assert.Equal(t, "backup", end["task"])
	assert.EqualValues(t, 7, end["exitCode"])
	assert.Equal(t, true, end["failed"])
	assert.EqualValues(t, jsonSchemaVersion, end["schemaVersion"])
}

func TestLogs_NoRunsYetIsNotAnError(t *testing.T) {
	slogBuf := captureCLISlog(t)
	d := newFakeLogDaemon()
	d.addTask("fresh", model.KindTask, 0)

	var out, errOut syncBuffer
	require.NoError(t, runLogsAgainst(t, t.Context(), d, &out, &errOut, false, "", false, "fresh"))
	assert.Empty(t, out.String())
	assert.Contains(t, slogBuf.String(), "No runs yet")
}

func TestLogs_UnknownTargets(t *testing.T) {
	d := newFakeLogDaemon()
	d.addTask("backup", model.KindTask, 0)

	var out, errOut syncBuffer
	err := runLogsAgainst(t, t.Context(), d, &out, &errOut, false, "", false, "bakup")
	require.ErrorContains(t, err, `Did you mean "backup"?`)

	err = runLogsAgainst(t, t.Context(), d, &out, &errOut, false, "", false, "01J8Z3K9QK6VN8XG2R5F7T1C4M")
	require.ErrorContains(t, err, "no run with ID 01J8Z3K9QK6VN8XG2R5F7T1C4M")
}

// Following only run IDs ends by itself once they end, replaying the -f
// backlog, and never opens the event stream.
func TestLogs_FollowRunIDExitsWhenItEnds(t *testing.T) {
	slogBuf := captureCLISlog(t)
	d := newFakeLogDaemon()
	d.addTask("backup", model.KindTask, 0)
	output := make([]string, 15)
	for i := range output {
		output[i] = "line " + strconv.Itoa(i)
	}
	d.addRun("01J00000000000000000000001", "backup", 0, model.ReasonSuccess, 0, output...)

	var out, errOut syncBuffer
	require.NoError(t, runLogsAgainst(t, t.Context(), d, &out, &errOut, true, "", false, "01J00000000000000000000001"))

	got := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	assert.Equal(t, output[15-followTailLines:], got, "-f replays the last 10 lines")
	assert.Contains(t, slogBuf.String(), "run succeeded")
	assert.Zero(t, d.eventHits, "a run-ID-only follow has no new runs to wait for")
}

// Following a name picks up runs that start later, streams them from their
// first line, and reports runs that ended without ever starting.
func TestLogs_FollowPicksUpNewRuns(t *testing.T) {
	slogBuf := captureCLISlog(t)
	d := newFakeLogDaemon()
	d.addTask("backup", model.KindTask, 0)
	d.addTask("other", model.KindTask, 0)

	started := d.addRun("01J00000000000000000000001", "backup", 0, model.ReasonSuccess, 0, "first", "second")
	missed := model.Run{ID: "01J00000000000000000000002", TaskName: "backup", Status: model.PhaseEnded, EndReason: model.EndReasonPtr(model.ReasonMissed), IsFailure: true}
	unrelated := d.addRun("01J00000000000000000000003", "other", 0, "", 0, "not mine")
	// The listing sees no runs of backup yet; they only arrive as events.
	d.unlisted[started.ID] = true
	d.events = []string{
		runEventFrame("run.started", started),
		runEventFrame("run.started", unrelated),
		runEventFrame("run.failed", missed),
	}

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var out, errOut syncBuffer
	errCh := make(chan error, 1)
	go func() { errCh <- runLogsAgainst(t, ctx, d, &out, &errOut, true, "", false, "back*") }()

	require.Eventually(t, func() bool {
		return strings.Contains(slogBuf.String(), "run succeeded") && strings.Contains(slogBuf.String(), "reason=missed")
	}, 5*time.Second, 10*time.Millisecond, "logs so far: %s", slogBuf.String())
	assert.Equal(t, "first\nsecond\n", out.String(), "a new run streams from its first line; other tasks stay out")

	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err, "Ctrl+C ends a follow cleanly")
	case <-time.After(5 * time.Second):
		t.Fatal("follow did not stop on cancel")
	}
}

// attachAgainst runs `runwisp <verb> --attach args...` against d until the
// CLI log shows every string in until, then interrupts it like Ctrl+C.
func attachAgainst(t *testing.T, d *fakeLogDaemon, v controlVerb, slogBuf *syncBuffer, until []string, args ...string) (stdout string, err error) {
	t.Helper()
	return runAttached(t, d, slogBuf, until, func(cmd *cobra.Command, f Flags) error {
		return controlTargets(cmd, f, remoteFlags{}, args, v, true)
	})
}

// runAttached serves d, starts run with a command whose output it captures,
// waits for the CLI log to show every string in until, then interrupts run
// like Ctrl+C and returns its stdout.
func runAttached(t *testing.T, d *fakeLogDaemon, slogBuf *syncBuffer, until []string, run func(*cobra.Command, Flags) error) (stdout string, err error) {
	t.Helper()
	f, _, _ := serveServiceSocket(t, d.handler())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var out, errOut syncBuffer
	cmd := &cobra.Command{}
	cmd.SetContext(ctx)
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	errCh := make(chan error, 1)
	go func() { errCh <- run(cmd, f) }()

	require.Eventually(t, func() bool {
		for _, s := range until {
			if !strings.Contains(slogBuf.String(), s) {
				return false
			}
		}
		return true
	}, 5*time.Second, 10*time.Millisecond, "logs so far: %s", slogBuf.String())
	cancel()
	select {
	case err = <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("attach did not stop on Ctrl+C")
	}
	return out.String(), err
}

// A restart --attach shows every line of the run it started, even one that
// ended before its stream was opened, and nothing of the run it replaced or of
// runs that finished earlier.
func TestAttach_RestartShowsOnlyNewRun(t *testing.T) {
	slogBuf := captureCLISlog(t)
	d := newFakeLogDaemon()
	d.addTask("backup", model.KindTask, 0)
	d.tasks[0].ManualTrigger = true
	d.addRun("01J00000000000000000000001", "backup", 0, model.ReasonFailed, 1, "stale output")
	d.addRun("01J00000000000000000000002", "backup", 0, "", 0, "old output")
	d.control = func(action, name string) int {
		if action != "restart" || name != "backup" {
			return http.StatusNotFound
		}
		stopped := d.end("01J00000000000000000000002", model.ReasonStopped)
		fresh := d.addRun("01J00000000000000000000003", "backup", 0, model.ReasonSuccess, 0, "new first", "new middle", "new last")
		d.events = append(d.events, runEventFrame("run.failed", stopped), runEventFrame("run.started", fresh), runEventFrame("run.completed", fresh))
		return http.StatusNoContent
	}

	out, err := attachAgainst(t, d, restartVerb, slogBuf, []string{"run succeeded"}, "backup")
	require.NoError(t, err, "Ctrl+C ends an attach cleanly")

	assert.Equal(t, "Task \"backup\" restarted.\nnew first\nnew middle\nnew last\n", out)
	assert.NotContains(t, slogBuf.String(), "01J00000000000000000000002", "the replaced run's end is not reported")
}

// A stop --attach shows the tail of the run it stopped and reports the stop
// as an end, not a failure.
func TestAttach_StopShowsStoppedRunEnd(t *testing.T) {
	slogBuf := captureCLISlog(t)
	d := newFakeLogDaemon()
	d.addTask("web", model.KindService, 1)
	d.tasks[0].ManualTrigger = true
	d.addRun("01J00000000000000000000001", "web", 0, "", 0, "serving")
	d.control = func(action, name string) int {
		if action != "stop" || name != "web" {
			return http.StatusNotFound
		}
		d.end("01J00000000000000000000001", model.ReasonStopped)
		return http.StatusNoContent
	}

	out, err := attachAgainst(t, d, stopVerb, slogBuf, []string{"reason=stopped"}, "web")
	require.NoError(t, err)
	assert.Equal(t, "Service \"web\" stopped.\nserving\n", out)
	assert.Contains(t, slogBuf.String(), `level=INFO msg="run ended"`)
}

// A glob --attach follows only what the verb acted on: not a locked task the
// glob skipped, not a target that failed, not a run that ended before. The
// failure still fails the command.
func TestAttach_FollowsOnlyTargetsActedOn(t *testing.T) {
	slogBuf := captureCLISlog(t)
	d := newFakeLogDaemon()
	for _, name := range []string{"web", "worker", "locked"} {
		d.addTask(name, model.KindService, 1)
	}
	d.tasks[0].ManualTrigger, d.tasks[1].ManualTrigger = true, true
	d.addRun("01J00000000000000000000000", "web", 0, model.ReasonFailed, 1, "web crashed yesterday")
	d.addRun("01J00000000000000000000001", "worker", 0, "", 0, "worker running")
	d.addRun("01J00000000000000000000002", "locked", 0, "", 0, "locked running")
	d.control = func(action, name string) int {
		switch name {
		case "worker":
			return http.StatusConflict
		case "web":
			web := d.addRun("01J00000000000000000000003", "web", 0, model.ReasonSuccess, 0, "web up")
			locked := d.addRun("01J00000000000000000000004", "locked", 0, model.ReasonSuccess, 0, "locked again")
			d.events = append(d.events, runEventFrame("run.started", locked), runEventFrame("run.started", web))
			return http.StatusNoContent
		}
		return http.StatusNotFound
	}

	out, err := attachAgainst(t, d, startVerb, slogBuf, []string{"run succeeded"}, "*")
	require.ErrorContains(t, err, `cannot start "worker"`)
	assert.Equal(t, "Service \"web\" started.\nweb up\n", out)
	assert.Contains(t, slogBuf.String(), "Following only the targets that succeeded")
}

// --attach on a daemon-wide stop or restart is rejected before anything stops.
func TestAttach_NeedsTarget(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	f := Flags{DataDir: t.TempDir()}
	require.ErrorIs(t, runStop(cmd, nil, f, true), errAttachNeedsTarget)
	require.ErrorIs(t, runRestart(cmd, nil, f, true), errAttachNeedsTarget)
}

// The start, stop and restart commands hand a parsed --attach to the control
// path: each follows the run its action starts.
func TestAttach_FlagReachesEachVerb(t *testing.T) {
	t.Setenv("RUNWISP_URL", "")
	savedFlags := flags
	t.Cleanup(func() { flags, controlAttach = savedFlags, false })

	for _, c := range []*cobra.Command{startCmd, stopCmd, restartCmd} {
		t.Run(c.Name(), func(t *testing.T) {
			slogBuf := captureCLISlog(t)
			d := newFakeLogDaemon()
			d.addTask("fast", model.KindTask, 0)
			d.tasks[0].ManualTrigger = true
			d.control = func(_, _ string) int {
				run := d.addRun("01J00000000000000000000001", "fast", 0, model.ReasonSuccess, 0, "fast output")
				d.events = append(d.events, runEventFrame("run.started", run))
				return http.StatusNoContent
			}
			controlAttach = false
			require.NoError(t, c.ParseFlags([]string{"--attach"}))

			out, err := runAttached(t, d, slogBuf, []string{"run succeeded"}, func(cmd *cobra.Command, f Flags) error {
				flags = f
				c.SetContext(cmd.Context())
				c.SetOut(cmd.OutOrStdout())
				c.SetErr(cmd.ErrOrStderr())
				t.Cleanup(func() { c.SetContext(nil); c.SetOut(nil); c.SetErr(nil) })
				return c.RunE(c, []string{"fast"})
			})
			require.NoError(t, err)
			assert.Contains(t, out, "fast output\n")
		})
	}
}
