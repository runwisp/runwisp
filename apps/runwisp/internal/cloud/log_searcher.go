// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"context"

	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/logsearch"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
)

// searchExecutionLog greps one run's on-disk log for lines matching query and
// returns them as protocol hit items. It mirrors readExecutionLogReplay but
// filters by a matcher instead of a line window, reusing the same logsearch
// engine the daemon's local REST search uses (logsearch.ScanRun).
//
// fromLine resumes a paginated scan: lines with absolute number <= fromLine are
// skipped. more reports whether the hit budget was reached before EOF — i.e.
// the run still has unscanned bytes the caller can page into via nextLine.
// A nil run yields no hits and exhausted=true: there is nothing on disk to scan
// (the dispatch may not have reached this daemon, or the run was deleted).
func searchExecutionLog(ctx context.Context, run *model.Run, logDir, query string, regex, caseSensitive bool, limit, fromLine int64) (hits []protocol.HitsItem, nextLine int64, exhausted bool, err error) {
	if run == nil {
		return nil, 0, true, nil
	}
	matcher, merr := logsearch.NewMatcher(query, regex, caseSensitive)
	if merr != nil {
		return nil, 0, false, &CloudError{Kind: CloudErrorKindValidation, Message: merr.Error()}
	}

	ref := logsearch.RunRef{
		ID:        run.ID,
		LogPath:   logutil.ResolveRunLogPath(logDir, run.TaskName, run.ID, run.CreatedAt),
		CreatedAt: run.CreatedAt,
	}
	found, more, serr := logsearch.ScanRun(ctx, ref, matcher, int(limit), fromLine)
	if serr != nil {
		return nil, 0, false, serr
	}

	hits = make([]protocol.HitsItem, len(found))
	for i, h := range found {
		stream := hitsItemStreamFromString(h.Stream)
		hits[i] = protocol.HitsItem{
			N:      h.N,
			Ts:     h.TS,
			Stream: &stream,
			Text:   h.Text,
		}
	}
	if more && len(found) > 0 {
		// Resume after the last emitted hit so the next page never re-emits it.
		nextLine = found[len(found)-1].N
	}
	return hits, nextLine, !more, nil
}
