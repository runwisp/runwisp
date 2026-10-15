// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/apps/runwisp/internal/server"
	"github.com/runwisp/runwisp/apps/runwisp/internal/server/logstream"
)

// followPace paces followRunLog's re-opens of a run's log stream.
//
// maxStalls bounds consecutive opens that find nothing to stream: a 404 or a
// stream that closes at once with no line and no Done event. A just-triggered
// run is handed back to the caller before its row is durably persisted
// (persistence is async), so the first stream(s) can find no run yet. We retry
// until the run becomes streamable; the bound only guards against a run ID that
// never materializes at all. stallBackoff paces those retries so the
// not-yet-persisted row has time to land without busy-looping.
//
// quietAfter separates a stall from a quiet run: the server closes every log
// stream after server.LogStreamTimeout, so a run that prints nothing for that
// long also yields an empty stream. One that stayed open this long was a live
// connection, not a refusal, and must not spend the stall budget; otherwise a
// run silent for maxStalls × LogStreamTimeout would be given up on while still
// running.
type followPace struct {
	maxStalls    int
	stallBackoff time.Duration
	quietAfter   time.Duration
}

var defaultFollowPace = followPace{maxStalls: 50, stallBackoff: 100 * time.Millisecond, quietAfter: time.Second}

// errLogStreamStalled reports that the run's log stream kept closing empty
// until the stall budget ran out.
var errLogStreamStalled = errors.New("log stream never became available")

// followRunLog streams runID's log from line `from` (negative counts from the
// end) to onLine until the run ends, re-opening the SSE stream across transport
// blips and the server's per-connection timeout without repeating or losing a
// line. Lines the daemon dropped are warned about under taskName. It returns
// done=true once the stream reports the run ended, and done=false with a nil
// error when ctx is cancelled.
func followRunLog(ctx context.Context, client *apiclient.Client, taskName, runID string, from int64, pace followPace, onLine func(server.LogLineEntry)) (bool, error) {
	stalls := 0
	for {
		opened := time.Now()
		highest, done, err := followOnce(ctx, client, taskName, runID, from, onLine)
		if ctx.Err() != nil {
			return false, nil // interrupted — stop reconnecting
		}
		if done {
			return true, err
		}

		switch {
		case highest >= from:
			// Progress: the stream closed mid-run (server timeout, or a transport
			// blip under heavy load) after delivering lines. Re-open from the next
			// unseen line so nothing is dropped or repeated. Progress resets the
			// stall budget.
			from = highest + 1
			stalls = 0
			continue
		case time.Since(opened) >= pace.quietAfter:
			// A quiet run: the connection was live, it just had nothing to say.
			stalls = 0
			continue
		}

		// Stall: the stream closed at once without a line or a Done event. The
		// run is not streamable yet — it was just triggered and its row lags
		// the trigger response — so back off and retry until the row lands.
		// Bailing on the first one would swallow the run's output silently.
		stalls++
		if stalls > pace.maxStalls {
			return false, errLogStreamStalled
		}
		select {
		case <-ctx.Done():
			return false, nil
		case <-time.After(pace.stallBackoff):
		}
	}
}

// followOnce opens one log stream and drains it. done means the loop must stop
// and err explains why: the run ended (err is a stream error, if any) or the
// stream could not be opened. A 404 is not an error here: the run's row has not
// landed yet, which counts as a stall (nothing streamed, not done).
func followOnce(ctx context.Context, client *apiclient.Client, taskName, runID string, from int64, onLine func(server.LogLineEntry)) (highest int64, done bool, err error) {
	ch, err := client.StreamLogLines(ctx, runID, from)
	if runNotPersistedYet(err) {
		return from - 1, false, nil
	}
	if err != nil {
		return from - 1, true, fmt.Errorf("open log stream: %w", err)
	}
	highest, done = drainLogStream(ch, taskName, runID, from, onLine)
	return highest, done, nil
}

// runNotPersistedYet reports whether a stream-open error is the daemon's 404
// for a run whose row has not landed yet (see followPace).
func runNotPersistedYet(err error) bool {
	var status *apiclient.HTTPStatusError
	return errors.As(err, &status) && status.StatusCode == http.StatusNotFound
}

// drainLogStream forwards one connection's lines to onLine until it closes.
// Lines below `from` were delivered on an earlier connection and are skipped.
// It returns the highest line number forwarded (from-1 when none) and whether
// the run ended. A read error ends the connection like a clean close would: a
// reverse proxy or NAT cutting a live stream is a transport blip, so the caller
// re-opens from the next unseen line instead of abandoning a run still going.
func drainLogStream(ch <-chan apiclient.LogStreamMsg, taskName, runID string, from int64, onLine func(server.LogLineEntry)) (highest int64, done bool) {
	highest = from - 1
	for msg := range ch {
		switch msg.Kind {
		case apiclient.LogStreamMsgKindLine:
			if msg.Line.N < from {
				continue
			}
			onLine(msg.Line)
			highest = max(highest, msg.Line.N)
		case apiclient.LogStreamMsgKindDropped:
			warnDroppedLines(taskName, runID, msg.Dropped)
		case apiclient.LogStreamMsgKindDone:
			return highest, true
		case apiclient.LogStreamMsgKindErr:
			slog.Debug("Log stream connection lost; re-opening", "run", runID, "err", msg.ErrValue)
			return highest, false
		}
	}
	return highest, false
}

// writeLogLine prints one captured line the way the task wrote it: its stderr
// lines to errOut, everything else (stdout, RunWisp's own system lines) to out.
// prefix labels the line's source when several runs share the output.
func writeLogLine(out, errOut io.Writer, stream, text, prefix string) {
	if stream == logutil.StreamStderr {
		out = errOut
	}
	fmt.Fprintln(out, prefix+text)
}

// warnDroppedLines surfaces lines the daemon dropped because this client read
// the stream too slowly — a gap in the output must never pass unremarked.
func warnDroppedLines(taskName, runID string, d logstream.DroppedEvent) {
	slog.Warn("Log lines dropped: the output was read too slowly",
		"task", taskName, "run", runID, "count", d.Count, "after_line", d.After)
}
