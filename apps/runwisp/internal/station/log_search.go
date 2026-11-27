// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/runwisp/runwisp/apps/runwisp/internal/crashguard"
	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logsearch"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
)

const (
	// grepFlushInterval is the longest a hit waits to go out, and how often
	// progress alone is reported while a grep runs.
	grepFlushInterval = 250 * time.Millisecond
	// grepMaxLineBytes cuts the text of each hit and context line.
	grepMaxLineBytes = 4 << 10
	grepMaxContext   = 5
	// grepClockLines is how many lines go by between looks at the clock.
	grepClockLines = 1024
	// grepConcurrency is how many many-run log:searches run at once. Each
	// takes a core and the disk while it lasts, and the daemon's runs come
	// first.
	grepConcurrency = 2
)

var errGrepTimeout = errors.New("log:search timed out")

// searchLogs serves log:search: it scans the station executions' local logs
// (one execution's, or many one run at a time, newest first), streaming hits
// as log:search:hits until every run is scanned, a cap or the timeout is
// reached, or cancel (or the session's end) stops it. Past grepConcurrency
// many-run searches it answers busy.
func (s *session) searchLogs(ctx context.Context, id string, req protocol.LogSearch) (any, error) {
	g, err := s.newGrepScan(id, req)
	if err != nil {
		return nil, err
	}
	if req.ExecutionID != "" {
		// One run's log is a bounded scan, like a log:subscribe replay, so it
		// takes no slot: a run's search never waits on a fleet-wide one.
		if g.run, err = s.client.lookupRun(ctx, req.ExecutionID); err != nil {
			return nil, err
		}
	} else {
		select {
		case s.client.grepSlots <- struct{}{}:
			defer func() { <-s.client.grepSlots }()
		default:
			return nil, &StationError{Code: protocol.ErrorCodeBusy, Message: fmt.Sprintf("%d log searches are already running", grepConcurrency), Retryable: true}
		}
	}
	// The scan runs on a goroutine of its own because lowerThreadPriority
	// keeps that goroutine's thread for good.
	var res any
	scanned := make(chan struct{})
	go func() {
		defer close(scanned)
		defer crashguard.Guard()
		lowerThreadPriority()
		res, err = s.grep(ctx, g, req)
	}()
	<-scanned
	return res, err
}

// grep runs scan g of log:search req.
func (s *session) grep(ctx context.Context, g *grepScan, req protocol.LogSearch) (any, error) {
	id := g.replyTo
	ctx, cancel := context.WithTimeoutCause(ctx, time.Duration(req.TimeoutMs)*time.Millisecond, errGrepTimeout)
	defer cancel()
	ctx, done := s.track(ctx, id)
	defer done()

	if g.run != nil {
		return s.grepRun(ctx, g, req)
	}
	// runsTotal is exact: the count reads only the index entries in range,
	// about 50 ms for a million runs, and the runs stream a page at a time.
	repo, q := s.client.deps.RunRepo, grepQuery(req, s.client.deps.TaskManager.ListServiceTasks())
	total, err := repo.CountGrepRuns(ctx, q)
	if err == nil {
		g.progress.RunsTotal = total
		err = repo.GrepRuns(ctx, q, func(run storage.GrepRun) bool {
			if g.over() || g.matched >= g.maxHits || ctx.Err() != nil {
				return false
			}
			executionID := executionIDOf(&model.Run{ID: run.ID, ExecutionID: run.ExecutionID})
			path := logutil.ResolveRunLogPath(s.client.deps.LogDir, run.TaskName, run.ID, run.CreatedAt)
			if err := g.scanRun(ctx, executionID, path, 0); err != nil && ctx.Err() == nil {
				// Retention may remove a log mid-scan; the other runs still count.
				slog.Debug("log:search skipped a run", "executionId", executionID, "err", err)
			}
			g.progress.RunsScanned++
			if time.Since(g.lastFlush) >= grepFlushInterval {
				g.flush()
			}
			return true
		})
	}
	if err != nil && ctx.Err() == nil {
		return nil, &StationError{Code: protocol.ErrorCodeInternal, Message: "failed to list the runs to search", Retryable: true, Err: err}
	}
	return g.finish(ctx)
}

// grepRun scans the one run g.run of log:search req from req.FromLine on.
// nextLine resumes it: after the last hit sent when a cap stopped it (no
// later line was matched), else after the last line read.
func (s *session) grepRun(ctx context.Context, g *grepScan, req protocol.LogSearch) (any, error) {
	g.progress.RunsTotal = 1
	path := logutil.ResolveRunLogPath(s.client.deps.LogDir, g.run.TaskName, g.run.ID, g.run.CreatedAt)
	if err := g.scanRun(ctx, req.ExecutionID, path, req.FromLine); err != nil && ctx.Err() == nil {
		slog.Debug("log:search read part of a run", "executionId", req.ExecutionID, "err", err)
	}
	g.progress.RunsScanned = 1
	res, err := g.finish(ctx)
	if err != nil {
		return nil, err
	}
	out := res.(protocol.LogSearchResult)
	switch out.Stopped {
	case protocol.StoppedDone:
	case protocol.StoppedMaxHits, protocol.StoppedMaxBytes:
		out.NextLine = g.lastSent + 1
	default:
		out.NextLine = max(g.readTo, req.FromLine)
	}
	return out, nil
}

// finish sends the last hits and builds the result.
func (g *grepScan) finish(ctx context.Context) (any, error) {
	if len(g.batch) > 0 {
		g.flush()
	}
	if g.closed {
		return nil, errSessionClosed
	}
	progress := g.progress
	return protocol.LogSearchResult{Progress: &progress, Hits: g.sent, Stopped: g.stopReason(ctx)}, nil
}

// grepScan is one log:search's matching, caps and batching.
type grepScan struct {
	sess     *session
	replyTo  string
	match    logsearch.Matcher
	run      *model.Run // the one run searched, nil when searching many
	context  int
	maxHits  int
	maxBytes int
	// budget is the bytes of hits one frame holds under maxFrameBytes.
	budget int

	progress protocol.LogSearchProgress
	matched  int   // hits found, counting those still gathering after-context
	sent     int   // hits queued for the station
	lastSent int64 // line of the last hit queued
	readTo   int64 // the line after the last one read
	bytes    int   // text bytes of the hits sent
	stop     protocol.Stopped
	closed   bool // the session ended

	batch      []protocol.LogSearchHit
	batchBytes int
	reported   protocol.LogSearchProgress // progress as of the last frame
	lastFlush  time.Time
	lines      int // lines read, to pace the clock

	buf    []byte   // the line scanner's buffer, shared by every run
	before grepRing // the run's last lines, for before-context
}

func (s *session) newGrepScan(id string, req protocol.LogSearch) (*grepScan, error) {
	if req.MaxHits <= 0 || req.TimeoutMs <= 0 {
		return nil, newError(protocol.ErrorCodeInvalid, "maxHits and timeoutMs must be positive")
	}
	matcher, err := searchMatcher(req)
	if err != nil {
		return nil, newError(protocol.ErrorCodeInvalid, "%s", err.Error())
	}
	// A frame's bytes beyond its hits: measured on the largest progress.
	empty, err := encodeFrame(typeLogSearchHits, "", id, protocol.LogSearchHits{Hits: []protocol.LogSearchHit{}, Progress: &protocol.LogSearchProgress{
		RunsScanned: math.MaxInt, RunsTotal: math.MaxInt, BytesScanned: math.MaxInt64,
	}})
	if err != nil {
		return nil, err
	}
	return &grepScan{
		sess:      s,
		replyTo:   id,
		match:     matcher,
		context:   min(max(req.Context, 0), grepMaxContext),
		maxHits:   req.MaxHits,
		maxBytes:  max(req.MaxBytes, 0),
		budget:    s.maxFrameBytes() - len(empty),
		lastFlush: time.Now(),
		buf:       make([]byte, logutil.ScanBufferSize),
		before:    grepRing{ring: make([]ringLine, min(max(req.Context, 0), grepMaxContext))},
	}, nil
}

// searchMatcher is req's matcher: its regex when set, else its terms.
func searchMatcher(req protocol.LogSearch) (logsearch.Matcher, error) {
	if req.Regex != "" {
		return logsearch.NewMatcher(req.Regex, true, req.CaseSensitive)
	}
	terms := make([]logsearch.Term, len(req.Terms))
	for i, t := range req.Terms {
		terms[i] = logsearch.Term{Parts: t.Parts, WordStart: t.WordStart, WordEnd: t.WordEnd}
	}
	return logsearch.NewTerms(terms, req.CaseSensitive)
}

// lookupRun finds the run of a station execution. One the daemon accepted
// but hasn't started yet is unavailable (retryable); an unknown one is
// not_found.
func (client *Client) lookupRun(ctx context.Context, executionID string) (*model.Run, error) {
	id, err := requireExecutionID(executionID)
	if err != nil {
		return nil, err
	}
	run, err := client.handler.findRun(ctx, id)
	switch {
	case err == nil && run != nil:
		return run, nil
	case err != nil && !errors.Is(err, storage.ErrNotFound):
		return nil, &StationError{Code: protocol.ErrorCodeInternal, Message: "failed to look up the execution", Retryable: true, Err: err}
	case client.tracker.Known(id):
		return nil, &StationError{Code: protocol.ErrorCodeUnavailable, Message: "execution has not started yet", Retryable: true}
	default:
		return nil, newError(protocol.ErrorCodeNotFound, "execution not found")
	}
}

// scanRun greps the log of one run from line from on, a missing log being an
// empty one. Once maxHits are found no new line matches, and the scan stops
// when the open hits have their after-context.
func (g *grepScan) scanRun(ctx context.Context, executionID, path string, from int64) error {
	g.before.reset()
	r := &runGrep{g: g, executionID: executionID, from: from}
	err := logutil.ScanLineBytes(ctx, path, g.buf, r.visit)
	for _, h := range r.open { // the log ended before their after-context did
		g.add(h)
	}
	return err
}

// runGrep is the scan of one run's log.
type runGrep struct {
	g           *grepScan
	executionID string
	from        int64                    // lines below it are context only
	open        []*protocol.LogSearchHit // hits still reading their after-context
}

// visit matches one line. text is only valid during the call: a line that
// becomes a hit or context is copied, every other line is not.
func (r *runGrep) visit(n int64, stream string, text []byte) bool {
	g := r.g
	if n < r.from {
		if n >= r.from-int64(g.context) { // only the lines before-context can reach
			g.before.push(n, stream, text)
		}
		return true
	}
	g.progress.BytesScanned += int64(len(text)) + 1
	g.readTo = n + 1
	if len(r.open) > 0 {
		r.extend(grepLine(n, stream, text))
	}
	if g.matched < g.maxHits && g.match.Match(logsearch.VisibleBytes(text)) {
		g.matched++
		line := grepLine(n, stream, text)
		h := &protocol.LogSearchHit{ExecutionID: r.executionID, Line: &line, Before: g.before.lines()}
		if g.context == 0 {
			g.add(h)
		} else {
			r.open = append(r.open, h)
		}
	}
	g.before.push(n, stream, text)
	// Reading the clock costs more than matching a short line.
	if g.lines++; g.lines%grepClockLines == 0 && time.Since(g.lastFlush) >= grepFlushInterval {
		g.flush()
	}
	return !g.over() && (g.matched < g.maxHits || len(r.open) > 0)
}

// extend adds line to the open hits' after-context and sends the hits it
// completes.
func (r *runGrep) extend(line protocol.LogLine) {
	kept := r.open[:0]
	for _, h := range r.open {
		h.After = append(h.After, line)
		if len(h.After) < r.g.context {
			kept = append(kept, h)
			continue
		}
		r.g.add(h)
	}
	r.open = kept
}

// add queues a complete hit, sending the batch first when the hit would push
// the frame past its budget. Reaching maxBytes stops the grep.
func (g *grepScan) add(h *protocol.LogSearchHit) {
	if g.over() {
		return
	}
	n := g.fit(h)
	if len(g.batch) > 0 && g.batchBytes+n > g.budget {
		g.flush()
	}
	g.batch = append(g.batch, *h)
	g.batchBytes += n + 1 // and its comma
	g.sent++
	g.lastSent = h.Line.N
	g.bytes += hitTextBytes(h)
	if g.maxBytes > 0 && g.bytes >= g.maxBytes {
		g.stop = protocol.StoppedMaxBytes
	}
}

// fit returns h's encoded size, first dropping its outermost context lines
// until it fits in a frame on its own.
func (g *grepScan) fit(h *protocol.LogSearchHit) int {
	for {
		raw, err := json.Marshal(h)
		if err != nil || len(raw) <= g.budget || len(h.Before)+len(h.After) == 0 {
			return len(raw)
		}
		if len(h.Before) >= len(h.After) {
			h.Before = h.Before[1:]
		} else {
			h.After = h.After[:len(h.After)-1]
		}
	}
}

// flush sends the batched hits with the current progress, unless there is
// nothing new since the last frame.
func (g *grepScan) flush() {
	g.lastFlush = time.Now()
	if g.closed || (len(g.batch) == 0 && g.progress == g.reported) {
		return
	}
	hits := g.batch
	if hits == nil {
		hits = []protocol.LogSearchHit{}
	}
	progress := g.progress
	if err := g.sess.send(typeLogSearchHits, "", g.replyTo, protocol.LogSearchHits{Hits: hits, Progress: &progress}, false); err != nil {
		g.closed = true
	}
	g.batch, g.batchBytes, g.reported = nil, 0, progress
}

// over reports that the grep must stop: maxBytes was reached or the session
// ended.
func (g *grepScan) over() bool {
	return g.stop != "" || g.closed
}

func (g *grepScan) stopReason(ctx context.Context) protocol.Stopped {
	switch {
	case g.stop != "":
		return g.stop
	case g.matched >= g.maxHits:
		return protocol.StoppedMaxHits
	case errors.Is(context.Cause(ctx), errGrepTimeout):
		return protocol.StoppedTimeout
	case ctx.Err() != nil:
		return protocol.StoppedCancelled
	default:
		return protocol.StoppedDone
	}
}

// grepLine is a hit or context line, its text cut to grepMaxLineBytes on a
// rune boundary.
func grepLine(n int64, stream string, text []byte) protocol.LogLine {
	line := protocol.LogLine{N: n, Stream: protocol.LogStream(stream)}
	if len(text) > grepMaxLineBytes {
		cut := grepMaxLineBytes
		for cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		text, line.Continued = text[:cut], true
	}
	line.Text = string(text)
	return line
}

// grepRing holds a run's last lines for before-context in buffers it reuses,
// each line's text cut to the grepMaxLineBytes+1 bytes grepLine needs to cut
// and flag it.
type grepRing struct {
	ring      []ringLine
	next, len int
}

type ringLine struct {
	n      int64
	stream string
	text   []byte
}

func (r *grepRing) reset() { r.next, r.len = 0, 0 }

func (r *grepRing) push(n int64, stream string, text []byte) {
	if len(r.ring) == 0 {
		return
	}
	l := &r.ring[r.next]
	l.n, l.stream = n, stream
	l.text = append(l.text[:0], text[:min(len(text), grepMaxLineBytes+1)]...)
	r.next = (r.next + 1) % len(r.ring)
	r.len = min(r.len+1, len(r.ring))
}

// lines returns the held lines oldest first, or nil if there are none.
func (r *grepRing) lines() []protocol.LogLine {
	if r.len == 0 {
		return nil
	}
	out := make([]protocol.LogLine, r.len)
	for i := range out {
		l := &r.ring[(r.next-r.len+i+len(r.ring))%len(r.ring)]
		out[i] = grepLine(l.n, l.stream, l.text)
	}
	return out
}

func hitTextBytes(h *protocol.LogSearchHit) int {
	n := len(h.Line.Text)
	for _, l := range h.Before {
		n += len(l.Text)
	}
	for _, l := range h.After {
		n += len(l.Text)
	}
	return n
}

// grepQuery selects the runs log:search req scans: the station executions,
// dispatched or of services (the station hears of a service instance's run,
// see EventBridge.handleRunEvent). until is inclusive to the millisecond, so
// the exclusive bound is the millisecond after it.
func grepQuery(req protocol.LogSearch, services []*model.Task) storage.GrepRunsQuery {
	q := storage.GrepRunsQuery{TaskNames: grepTaskNames(req.Tasks)}
	for _, svc := range services {
		q.ServiceTasks = append(q.ServiceTasks, svc.Name)
	}
	if req.Since > 0 {
		q.Since = time.UnixMilli(req.Since)
	}
	if req.Until > 0 {
		q.Before = time.UnixMilli(req.Until + 1)
	}
	return q
}

// grepTaskNames maps log:search's tasks to the run task names they cover: an
// entry is a task name of the daemon's own (a config dispatch's runs and a
// service instance's carry it; the station knows a service run's task by it,
// see executionUpdate), or a station task's TaskRef.id, whose inline
// dispatches and services file their runs under sanitizeStationTaskName(id).
// nil means every task.
func grepTaskNames(tasks []string) []string {
	var names []string
	for _, t := range tasks {
		if t = strings.TrimSpace(t); t != "" {
			names = append(names, t, sanitizeStationTaskName(t))
		}
	}
	return names
}
