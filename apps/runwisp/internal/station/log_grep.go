// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logsearch"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
)

const (
	// grepFlushInterval is the longest a hit waits to go out, and how often
	// progress alone is reported while a grep runs.
	grepFlushInterval = 250 * time.Millisecond
	// grepMaxLineBytes cuts the text of each hit and context line.
	grepMaxLineBytes = 4 << 10
	grepMaxContext   = 5
)

var errGrepTimeout = errors.New("log:grep timed out")

// grepLogs serves log:grep: it scans the station executions' local logs one
// run at a time, newest first, streaming hits as log:grep:hits until every
// run is scanned, a cap or the timeout is reached, or log:grep:cancel (or the
// session's end) stops it.
func (s *session) grepLogs(ctx context.Context, id string, req protocol.LogGrep) (any, error) {
	g, err := s.newGrepScan(id, req)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeoutCause(ctx, time.Duration(req.TimeoutMs)*time.Millisecond, errGrepTimeout)
	defer cancel()
	ctx, done := s.track(ctx, id)
	defer done()

	// runsTotal is exact: the count reads only the index entries in range,
	// about 50 ms for a million runs, and the runs stream a page at a time.
	repo, q := s.client.deps.RunRepo, grepQuery(req)
	total, err := repo.CountGrepRuns(ctx, q)
	if err == nil {
		g.progress.RunsTotal = total
		err = repo.GrepRuns(ctx, q, func(run storage.GrepRun) bool {
			if g.over() || g.matched >= g.maxHits || ctx.Err() != nil {
				return false
			}
			path := logutil.ResolveRunLogPath(s.client.deps.LogDir, run.TaskName, run.ID, run.CreatedAt)
			if err := g.scanRun(ctx, run.ExecutionID, path); err != nil && ctx.Err() == nil {
				// Retention may remove a log mid-scan; the other runs still count.
				slog.Debug("log:grep skipped a run", "executionId", run.ExecutionID, "err", err)
			}
			g.progress.RunsScanned++
			if len(g.batch) > 0 || time.Since(g.lastFlush) >= grepFlushInterval {
				g.flush()
			}
			return true
		})
	}
	if err != nil && ctx.Err() == nil {
		return nil, &StationError{Code: protocol.ErrorCodeInternal, Message: "failed to list the runs to search", Retryable: true, Err: err}
	}
	if len(g.batch) > 0 {
		g.flush()
	}
	if g.closed {
		return nil, errSessionClosed
	}
	progress := g.progress
	return protocol.LogGrepResult{Progress: &progress, Hits: g.sent, Stopped: g.stopReason(ctx)}, nil
}

// grepScan is one log:grep's matching, caps and batching.
type grepScan struct {
	sess     *session
	replyTo  string
	terms    *logsearch.Terms
	context  int
	maxHits  int
	maxBytes int
	// budget is the bytes of hits one frame holds under maxFrameBytes.
	budget int

	progress protocol.LogGrepProgress
	matched  int // hits found, counting those still gathering after-context
	sent     int // hits queued for the station
	bytes    int // text bytes of the hits sent
	stop     protocol.Stopped
	closed   bool // the session ended

	batch      []protocol.LogGrepHit
	batchBytes int
	reported   protocol.LogGrepProgress // progress as of the last frame
	lastFlush  time.Time
}

func (s *session) newGrepScan(id string, req protocol.LogGrep) (*grepScan, error) {
	if req.MaxHits <= 0 || req.TimeoutMs <= 0 {
		return nil, newError(protocol.ErrorCodeInvalid, "maxHits and timeoutMs must be positive")
	}
	terms := make([]logsearch.Term, len(req.Terms))
	for i, t := range req.Terms {
		terms[i] = logsearch.Term{Parts: t.Parts, WordStart: t.WordStart, WordEnd: t.WordEnd}
	}
	matcher, err := logsearch.NewTerms(terms, req.CaseSensitive)
	if err != nil {
		return nil, newError(protocol.ErrorCodeInvalid, "%s", err.Error())
	}
	// A frame's bytes beyond its hits: measured on the largest progress.
	empty, err := encodeFrame(typeLogGrepHits, "", id, protocol.LogGrepHits{Hits: []protocol.LogGrepHit{}, Progress: &protocol.LogGrepProgress{
		RunsScanned: math.MaxInt, RunsTotal: math.MaxInt, BytesScanned: math.MaxInt64,
	}})
	if err != nil {
		return nil, err
	}
	return &grepScan{
		sess:      s,
		replyTo:   id,
		terms:     matcher,
		context:   min(max(req.Context, 0), grepMaxContext),
		maxHits:   req.MaxHits,
		maxBytes:  max(req.MaxBytes, 0),
		budget:    s.maxFrameBytes() - len(empty),
		lastFlush: time.Now(),
	}, nil
}

// scanRun greps the log of one run, a missing log being an empty one. Once
// maxHits are found no new line matches, and the scan stops when the open
// hits have their after-context.
func (g *grepScan) scanRun(ctx context.Context, executionID, path string) error {
	r := &runGrep{g: g, executionID: executionID}
	err := logutil.ScanLines(ctx, path, r.visit)
	for _, h := range r.open { // the log ended before their after-context did
		g.add(h)
	}
	return err
}

// runGrep is the scan of one run's log.
type runGrep struct {
	g           *grepScan
	executionID string
	before      []protocol.LogLine     // the last context lines, oldest first
	open        []*protocol.LogGrepHit // hits still reading their after-context
}

func (r *runGrep) visit(rec logutil.LogLineRecord) bool {
	g := r.g
	g.progress.BytesScanned += int64(len(rec.Text)) + 1
	line := grepLine(rec)
	r.extend(line)
	if g.matched < g.maxHits && g.terms.Match(logsearch.VisibleText(rec.Text)) {
		g.matched++
		h := &protocol.LogGrepHit{ExecutionID: r.executionID, Line: &line, Before: slices.Clone(r.before)}
		if g.context == 0 {
			g.add(h)
		} else {
			r.open = append(r.open, h)
		}
	}
	if g.context > 0 {
		r.before = append(r.before, line)
		r.before = r.before[max(len(r.before)-g.context, 0):]
	}
	if time.Since(g.lastFlush) >= grepFlushInterval {
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
func (g *grepScan) add(h *protocol.LogGrepHit) {
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
	g.bytes += hitTextBytes(h)
	if g.maxBytes > 0 && g.bytes >= g.maxBytes {
		g.stop = protocol.StoppedMaxBytes
	}
}

// fit returns h's encoded size, first dropping its outermost context lines
// until it fits in a frame on its own.
func (g *grepScan) fit(h *protocol.LogGrepHit) int {
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
		hits = []protocol.LogGrepHit{}
	}
	progress := g.progress
	if err := g.sess.send(typeLogGrepHits, "", g.replyTo, protocol.LogGrepHits{Hits: hits, Progress: &progress}, false); err != nil {
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

// grepLine is rec as a hit or context line, its text cut to grepMaxLineBytes
// on a rune boundary.
func grepLine(rec logutil.LogLineRecord) protocol.LogLine {
	line := protocol.LogLine{N: rec.LineNum, Stream: protocol.LogStream(rec.Stream), Text: rec.Text}
	if len(line.Text) > grepMaxLineBytes {
		cut := grepMaxLineBytes
		for cut > 0 && !utf8.RuneStart(line.Text[cut]) {
			cut--
		}
		line.Text, line.Continued = line.Text[:cut], true
	}
	return line
}

func hitTextBytes(h *protocol.LogGrepHit) int {
	n := len(h.Line.Text)
	for _, l := range h.Before {
		n += len(l.Text)
	}
	for _, l := range h.After {
		n += len(l.Text)
	}
	return n
}

// grepQuery selects the runs log:grep req scans. until is inclusive to the
// millisecond, so the exclusive bound is the millisecond after it.
func grepQuery(req protocol.LogGrep) storage.GrepRunsQuery {
	q := storage.GrepRunsQuery{TaskNames: grepTaskNames(req.Tasks)}
	if req.Since > 0 {
		q.Since = time.UnixMilli(req.Since)
	}
	if req.Until > 0 {
		q.Before = time.UnixMilli(req.Until + 1)
	}
	return q
}

// grepTaskNames maps log:grep's tasks to the run task names they cover: an
// entry is a task name of the daemon's own (a config dispatch's runs carry
// it), or a station task's TaskRef.id, whose inline dispatches file their
// runs under sanitizeStationTaskName(id). nil means every task.
func grepTaskNames(tasks []string) []string {
	var names []string
	for _, t := range tasks {
		if t = strings.TrimSpace(t); t != "" {
			names = append(names, t, sanitizeStationTaskName(t))
		}
	}
	return names
}
