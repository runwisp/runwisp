// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
)

// readExecutionLogReplay reads a bounded historical page of log lines for an
// execution. Returns the lines (already encoded as protocol items) plus a
// `final` flag — true when no more lines exist beyond this page or the run
// has reached a terminal status.
func readExecutionLogReplay(run *sqlcdb.Run, logDir string, fromLine, limit int64) ([]protocol.LinesItem, bool, error) {
	if run == nil {
		return nil, true, nil
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

	items := make([]protocol.LinesItem, len(records))
	for i, r := range records {
		stream := linesItemStreamFromString(r.Stream)
		items[i] = protocol.LinesItem{
			N:      r.LineNum,
			Stream: &stream,
			Text:   r.Text,
		}
	}

	finalCursor := fromLine + int64(len(items))
	final := finalCursor >= totalLines && run.Status.IsTerminal()
	return items, final, nil
}
