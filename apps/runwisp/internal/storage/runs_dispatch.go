// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package storage

import (
	"context"
	"fmt"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
)

// queryRunsSortKey collapses a (column, direction) tuple into the discrete
// dispatch key used by dispatchQueryRuns. The default column (empty input)
// falls back to created_at — so the most recent rows surface first by default,
// while an explicit direction (e.g. the runs-list sort toggle, which sets only
// the direction) still flips the order against that same column.
func queryRunsSortKey(col SortColumn, dir SortDirection) string {
	column := col
	if column == SortColumnDefault {
		column = SortColumnCreatedAt
	}
	direction := "desc"
	if dir == SortAsc {
		direction = "asc"
	}
	return string(column) + "_" + direction
}

// dispatchQueryRuns picks one of the 12 sqlc-generated QueryRuns variants
// keyed by the sort tuple. The variants differ only in ORDER BY, so their
// param types all convert from the shared QueryRunsCreatedAtAscParams.
func dispatchQueryRuns(
	ctx context.Context,
	q *sqlcdb.Queries,
	col SortColumn,
	dir SortDirection,
	params sqlcdb.QueryRunsCreatedAtAscParams,
) ([]model.Run, error) {
	switch queryRunsSortKey(col, dir) {
	case "createdAt_asc":
		return finishQueryRuns(q.QueryRunsCreatedAtAsc(ctx, params))
	case "createdAt_desc":
		return finishQueryRuns(q.QueryRunsCreatedAtDesc(ctx, sqlcdb.QueryRunsCreatedAtDescParams(params)))
	case "startedAt_asc":
		return finishQueryRuns(q.QueryRunsStartAtAsc(ctx, sqlcdb.QueryRunsStartAtAscParams(params)))
	case "startedAt_desc":
		return finishQueryRuns(q.QueryRunsStartAtDesc(ctx, sqlcdb.QueryRunsStartAtDescParams(params)))
	case "taskName_asc":
		return finishQueryRuns(q.QueryRunsTaskNameAsc(ctx, sqlcdb.QueryRunsTaskNameAscParams(params)))
	case "taskName_desc":
		return finishQueryRuns(q.QueryRunsTaskNameDesc(ctx, sqlcdb.QueryRunsTaskNameDescParams(params)))
	case "status_asc":
		return finishQueryRuns(q.QueryRunsStatusAsc(ctx, sqlcdb.QueryRunsStatusAscParams(params)))
	case "status_desc":
		return finishQueryRuns(q.QueryRunsStatusDesc(ctx, sqlcdb.QueryRunsStatusDescParams(params)))
	case "exitCode_asc":
		return finishQueryRuns(q.QueryRunsExitCodeAsc(ctx, sqlcdb.QueryRunsExitCodeAscParams(params)))
	case "exitCode_desc":
		return finishQueryRuns(q.QueryRunsExitCodeDesc(ctx, sqlcdb.QueryRunsExitCodeDescParams(params)))
	case "duration_asc":
		return finishQueryRuns(q.QueryRunsDurationAsc(ctx, sqlcdb.QueryRunsDurationAscParams(params)))
	case "duration_desc":
		return finishQueryRuns(q.QueryRunsDurationDesc(ctx, sqlcdb.QueryRunsDurationDescParams(params)))
	}
	return nil, fmt.Errorf("unknown sort column %q", col)
}

// finishQueryRuns turns a sqlc row slice and the call's error into domain runs.
func finishQueryRuns(rows []sqlcdb.Run, err error) ([]model.Run, error) {
	if err != nil {
		return nil, err
	}
	return runsFromRows(rows), nil
}
