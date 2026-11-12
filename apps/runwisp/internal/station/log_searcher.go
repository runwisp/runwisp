// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"context"
	"errors"

	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logsearch"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
)

const (
	defaultSearchLimit = 100
	maxSearchLimit     = 1000
)

// lineFrameOverhead is a generous estimate of one log line's JSON framing
// beyond its text, for keeping a frame under maxFrameBytes.
const lineFrameOverhead = 96

// searchLog answers log:search: one page of the execution's matching lines,
// cut short (exhausted false, nextLine set) to fit the frame budget.
func (client *Client) searchLog(ctx context.Context, q protocol.LogSearch, maxFrameBytes int) (protocol.LogSearchResult, error) {
	run, err := client.lookupRun(ctx, q.ExecutionID)
	if err != nil {
		return protocol.LogSearchResult{}, err
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	return searchExecutionLog(ctx, run, client.deps.LogDir, q.Query, q.Regex, q.CaseSensitive, min(limit, maxSearchLimit), q.FromLine, maxFrameBytes/2)
}

// lookupRun finds the run of a station execution. One the daemon accepted
// but hasn't started yet is unavailable (retryable); an unknown one is
// not_found.
func (client *Client) lookupRun(ctx context.Context, executionID string) (*model.Run, error) {
	id, err := requireExecutionID(executionID)
	if err != nil {
		return nil, err
	}
	run, err := client.deps.RunRepo.GetRunByExecutionID(ctx, id)
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

// searchExecutionLog greps one run's on-disk log with the same engine the
// daemon's local search uses. fromLine resumes a paginated scan; budget caps
// the hits' estimated encoded size.
func searchExecutionLog(ctx context.Context, run *model.Run, logDir, query string, regex, caseSensitive bool, limit, fromLine int64, budget int) (protocol.LogSearchResult, error) {
	matcher, err := logsearch.NewMatcher(query, regex, caseSensitive)
	if err != nil {
		return protocol.LogSearchResult{}, newError(protocol.ErrorCodeInvalid, "%s", err.Error())
	}
	ref := logsearch.RunRef{
		ID:        run.ID,
		LogPath:   logutil.ResolveRunLogPath(logDir, run.TaskName, run.ID, run.CreatedAt),
		CreatedAt: run.CreatedAt,
	}
	found, more, err := logsearch.ScanRun(ctx, ref, matcher, int(limit), fromLine)
	if err != nil {
		return protocol.LogSearchResult{}, &StationError{Code: protocol.ErrorCodeInternal, Message: "failed to search the execution log", Retryable: true, Err: err}
	}

	out := protocol.LogSearchResult{Hits: []protocol.LogLine{}, Exhausted: !more}
	size := 0
	for i, h := range found {
		size += len(h.Text) + lineFrameOverhead
		if i > 0 && size > budget {
			more, out.Exhausted = true, false
			break
		}
		out.Hits = append(out.Hits, protocol.LogLine{N: h.N, Ts: h.TS, Stream: protocol.LogStream(h.Stream), Text: h.Text})
	}
	if more && len(out.Hits) > 0 {
		// Resume after the last hit sent, so the next page never repeats it.
		out.NextLine = out.Hits[len(out.Hits)-1].N + 1
	}
	return out, nil
}
