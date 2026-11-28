// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"context"
	"errors"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/events"
	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

const (
	// liveLineBuffer bounds the live lines held for a subscription that can't
	// keep up. On overflow the oldest are dropped; they are on disk, so the
	// subscription reads them back from there and nothing is lost.
	liveLineBuffer = 4096
	// logBatchWindow coalesces live lines into one log:lines frame: short
	// enough to feel instant, long enough to collapse a burst.
	logBatchWindow = 50 * time.Millisecond
	// diskReadLines is how many lines one replay read takes from disk.
	diskReadLines = 1000
)

// logSubscription is one log:subscribe: it sends lines from next on, in
// order, each exactly once.
type logSubscription struct {
	sess    *session
	replyTo string
	path    string
	next    int64 // the next line to send
	limit   int64 // 0 = none
	sent    int64
	batch   []protocol.LogLine
	size    int
	budget  int
}

// streamLog serves log:subscribe: replay from disk, then (with follow) the
// live tail. The bus subscription starts before the disk read, and every line
// is on disk before it is published, so the two meet without a gap: a live
// line past next means lines in between are on disk, and the subscription
// reads them from there. Only lines no longer on disk (rotated away) are
// reported as a gap.
func (s *session) streamLog(ctx context.Context, id string, req protocol.LogSubscribe) (any, error) {
	run, err := s.client.lookupRun(ctx, req.ExecutionID)
	if err != nil {
		return nil, err
	}
	ctx, done := s.track(ctx, id)
	defer done()
	return s.streamRun(ctx, id, run, req.FromLine, req.Limit, req.Follow)
}

// track registers command id (a log:subscribe or log:search) so
// cancel can end it; done unregisters it.
func (s *session) track(ctx context.Context, id string) (_ context.Context, done func()) {
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.subs[id] = cancel
	s.mu.Unlock()
	return ctx, func() {
		s.mu.Lock()
		delete(s.subs, id)
		s.mu.Unlock()
		cancel()
	}
}

// streamRun streams one run's log for subscription id: from, limit and follow
// as in log:subscribe.
func (s *session) streamRun(ctx context.Context, id string, run *model.Run, from, limit int64, follow bool) (protocol.LogSubscribeResult, error) {
	path := logutil.ResolveRunLogPath(s.client.deps.LogDir, run.TaskName, run.ID, run.CreatedAt)
	sub := &logSubscription{
		sess:    s,
		replyTo: id,
		path:    path,
		next:    startLine(path, from),
		limit:   limit,
		budget:  s.params.maxFrameBytes / 2,
	}

	var live <-chan events.LogLineEvent
	var finished <-chan struct{}
	if follow && !run.Status.IsTerminal() {
		var stop func()
		live, finished, stop = followRun(s.client.deps.EventBus, run.ID)
		defer stop()
	}
	// The run may have ended before the subscriptions above existed.
	ended := run.Status.IsTerminal()
	if !ended {
		if fresh, err := s.client.deps.RunRepo.GetRun(ctx, run.ID); err == nil && fresh.Status.IsTerminal() {
			ended = true
		}
	}

	// replay stops short of the end only at the limit or on an error.
	_, err := sub.replay()
	switch {
	case err != nil:
		return sub.fail(err)
	case sub.full(), ended:
		return sub.end(protocol.LogSubscribeStopDone), nil
	case !follow:
		return sub.end(protocol.LogSubscribeStopCaughtUp), nil
	}
	return sub.tail(ctx, live, finished)
}

// startLine resolves a subscription's fromLine: a negative one asks for a
// tail, the last -fromLine lines on disk.
func startLine(path string, from int64) int64 {
	if from >= 0 {
		return from
	}
	_, first, total, err := logutil.ReadLineRange(path, 0, 1)
	if err != nil {
		return 0
	}
	return logutil.ResolveStartLine(from, first, total)
}

// followRun subscribes to a run's live lines and its end.
func followRun(bus EventSubscriber, runID string) (<-chan events.LogLineEvent, <-chan struct{}, func()) {
	live := make(chan events.LogLineEvent, liveLineBuffer)
	finished := make(chan struct{}, 1)
	onEnd := func(e events.Event) {
		if re, ok := e.Data.(events.RunEvent); ok && re.Run != nil && re.Run.ID == runID {
			select {
			case finished <- struct{}{}:
			default:
			}
		}
	}
	stops := []func(){
		bus.Subscribe(events.EventLogLine, func(e events.Event) {
			if le, ok := e.Data.(events.LogLineEvent); ok && le.RunID == runID {
				pushEvictingOldest(live, le)
			}
		}),
		bus.Subscribe(events.EventRunCompleted, onEnd),
		bus.Subscribe(events.EventRunFailed, onEnd),
	}
	return live, finished, func() {
		for _, stop := range stops {
			stop()
		}
	}
}

// tail sends live lines until the run ends, the limit is reached or the
// subscription is cancelled.
func (sub *logSubscription) tail(ctx context.Context, live <-chan events.LogLineEvent, finished <-chan struct{}) (protocol.LogSubscribeResult, error) {
	ticker := time.NewTicker(logBatchWindow)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return sub.end(protocol.LogSubscribeStopCancelled), nil
		case <-finished:
			// Every line is on disk before the run ends: read to the end.
			if _, err := sub.replay(); err != nil {
				return sub.fail(err)
			}
			return sub.end(protocol.LogSubscribeStopDone), nil
		case le := <-live:
			if !sub.addLive(le) {
				return sub.fail(errSessionClosed)
			}
			if sub.full() {
				return sub.end(protocol.LogSubscribeStopDone), nil // stops at the limit
			}
		case <-ticker.C:
			if !sub.flush() {
				return sub.fail(errSessionClosed)
			}
		}
	}
}

// unsubscribe ends the log:subscribe or log:search started by command id, if it
// still runs.
func (s *session) unsubscribe(id string) {
	s.mu.Lock()
	cancel := s.subs[id]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func pushEvictingOldest[T any](ch chan T, v T) {
	for {
		select {
		case ch <- v:
			return
		default:
		}
		select {
		case <-ch:
		default:
		}
	}
}

// replay sends the lines on disk from next on. caughtUp reports it reached
// the end of what is on disk (rather than stopping at the limit).
func (sub *logSubscription) replay() (caughtUp bool, err error) {
	for !sub.full() {
		records, firstAvailable, _, err := logutil.ReadLineRange(sub.path, sub.next, diskReadLines)
		if err != nil {
			return false, err
		}
		if sub.next < firstAvailable {
			sub.gap(firstAvailable)
		}
		before := sub.next
		if !sub.addRecords(records) {
			return false, errSessionClosed
		}
		if sub.next == before {
			return true, nil
		}
	}
	return false, nil
}

// addRecords adds the disk records from next on, up to the limit.
func (sub *logSubscription) addRecords(records []logutil.LogLineRecord) bool {
	for _, r := range records {
		if sub.full() {
			break
		}
		if r.LineNum >= sub.next && !sub.add(protocol.LogLine{N: r.LineNum, Stream: protocol.LogStream(r.Stream), Text: r.Text}) {
			return false
		}
	}
	return true
}

// addLive takes one live line. A line past next means the ones between were
// dropped from the live buffer: they are read back from disk first.
func (sub *logSubscription) addLive(le events.LogLineEvent) bool {
	if le.LineNum > sub.next {
		if _, err := sub.replay(); err != nil {
			return false
		}
		if sub.next < le.LineNum && !sub.full() {
			sub.gap(le.LineNum) // not on disk any more
		}
	}
	if le.LineNum < sub.next || sub.full() {
		return true
	}
	return sub.add(protocol.LogLine{N: le.LineNum, Ts: le.Timestamp, Stream: protocol.LogStream(le.Stream), Text: le.Text, Continued: le.Continued})
}

// lineFrameOverhead is a generous estimate of one log line's JSON framing
// beyond its text, for keeping a frame under maxFrameBytes.
const lineFrameOverhead = 96

// add appends a line to the batch, sending the batch first when the line
// would push it past the frame budget.
func (sub *logSubscription) add(line protocol.LogLine) bool {
	lineSize := len(line.Text) + lineFrameOverhead
	if len(sub.batch) > 0 && sub.size+lineSize > sub.budget && !sub.flush() {
		return false
	}
	sub.batch = append(sub.batch, line)
	sub.size += lineSize
	sub.next = line.N + 1
	sub.sent++
	return true
}

// gap reports that lines [next, to) can't be sent, then skips them.
func (sub *logSubscription) gap(to int64) {
	if !sub.flush() {
		return
	}
	_ = sub.sess.send(typeLogLines, "", sub.replyTo, protocol.LogLines{Lines: []protocol.LogLine{}, Gap: &protocol.LogGap{From: sub.next, To: to}})
	sub.next = to
}

func (sub *logSubscription) flush() bool {
	if len(sub.batch) == 0 {
		return true
	}
	err := sub.sess.send(typeLogLines, "", sub.replyTo, protocol.LogLines{Lines: sub.batch})
	// send encoded the batch already, so its array is free to reuse.
	sub.batch, sub.size = sub.batch[:0], 0
	return err == nil
}

func (sub *logSubscription) full() bool {
	return sub.limit > 0 && sub.sent >= sub.limit
}

// end sends what is batched and returns the subscription's result. A
// subscription that reached its limit stops at the limit, whatever else ended
// it.
func (sub *logSubscription) end(stopped protocol.LogSubscribeStop) protocol.LogSubscribeResult {
	sub.flush()
	if sub.full() {
		stopped = protocol.LogSubscribeStopLimit
	}
	return protocol.LogSubscribeResult{NextLine: sub.next, Stopped: stopped}
}

// fail ends the subscription on err. A closed session can't take a result,
// so only a log the daemon couldn't read is reported, as internal.
func (sub *logSubscription) fail(err error) (protocol.LogSubscribeResult, error) {
	if !errors.Is(err, errSessionClosed) {
		sub.flush()
	}
	return protocol.LogSubscribeResult{}, err
}
