// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"context"

	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logsearch"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

// logSearchParams bundles the query options for searchExecutionLog.
//
// fromLine resumes a paginated scan: it is the first line to scan, so 0 scans
// the whole run. limit caps hits per page.
type logSearchParams struct {
	query         string
	regex         bool
	caseSensitive bool
	limit         int64
	fromLine      int64
}

// searchExecutionLog greps one run's on-disk log for lines matching the query
// and returns them as protocol hit items. It mirrors readExecutionLogReplay but
// filters by a matcher instead of a line window, reusing the same logsearch
// engine the daemon's local REST search uses (logsearch.ScanRun).
//
// exhausted is false when the hit budget was reached before EOF, i.e. the run
// still has unscanned bytes the caller can page into via nextLine. A nil run
// yields no hits and exhausted=true: there is nothing on disk to scan (the
// dispatch may not have reached this daemon, or the run was deleted). Errors
// are *StationError: a malformed query is a validation error, a scan failure
// is transient.
func searchExecutionLog(ctx context.Context, run *model.Run, logDir string, p logSearchParams) (hits []protocol.LogLineEntry, nextLine int64, exhausted bool, err error) {
	if run == nil {
		return nil, 0, true, nil
	}
	matcher, merr := logsearch.NewMatcher(p.query, p.regex, p.caseSensitive)
	if merr != nil {
		return nil, 0, false, &StationError{Kind: StationErrorKindValidation, Message: merr.Error()}
	}

	ref := logsearch.RunRef{
		ID:        run.ID,
		LogPath:   logutil.ResolveRunLogPath(logDir, run.TaskName, run.ID, run.CreatedAt),
		CreatedAt: run.CreatedAt,
	}
	found, more, serr := logsearch.ScanRun(ctx, ref, matcher, int(p.limit), p.fromLine)
	if serr != nil {
		return nil, 0, false, &StationError{Kind: StationErrorKindTransient, Message: "failed to search execution logs", Err: serr}
	}

	hits = make([]protocol.LogLineEntry, len(found))
	for i, h := range found {
		hits[i] = protocol.LogLineEntry{
			N:      h.N,
			Ts:     h.TS,
			Stream: protocol.Stream(h.Stream),
			Text:   h.Text,
		}
	}
	if more && len(found) > 0 {
		// Resume after the last emitted hit so the next page never re-emits it.
		nextLine = found[len(found)-1].N + 1
	}
	return hits, nextLine, !more, nil
}

// readExecutionLogReplay reads a bounded historical page of log lines for an
// execution. Returns the lines (already encoded as protocol items) plus a
// `final` flag — true only when the page reaches the end of the log AND the
// run is terminal. A nil run is NOT final: the daemon may simply not have
// received the dispatch yet, and claiming final would end the viewer's
// stream on an execution that hasn't started.
func readExecutionLogReplay(run *model.Run, logDir string, fromLine, limit int64) ([]protocol.LogLineEntry, bool, error) {
	if run == nil {
		return nil, false, nil
	}
	if limit <= 0 {
		limit = maxProtocolLogLines
	}
	if limit > maxProtocolLogLines {
		limit = maxProtocolLogLines
	}

	logPath := logutil.ResolveRunLogPath(logDir, run.TaskName, run.ID, run.CreatedAt)
	records, _, totalLines, err := logutil.ReadLineRange(logPath, fromLine, limit)
	if err != nil {
		return nil, run.Status.IsTerminal(), err
	}

	items := make([]protocol.LogLineEntry, len(records))
	for i, r := range records {
		items[i] = protocol.LogLineEntry{
			N:      r.LineNum,
			Stream: protocol.Stream(r.Stream),
			Text:   r.Text,
		}
	}

	finalCursor := fromLine + int64(len(items))
	final := finalCursor >= totalLines && run.Status.IsTerminal()
	return items, final, nil
}
