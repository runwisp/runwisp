// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
// keyed by the sort tuple. The 12 row types are structurally identical
// (the SELECT list is shared); each case casts its row slice onto the
// shared QueryRunsCreatedAtAscRow type so a single conversion path produces
// the domain rows.
func dispatchQueryRuns(
	ctx context.Context,
	q *sqlcdb.Queries,
	col SortColumn,
	dir SortDirection,
	params sqlcdb.QueryRunsCreatedAtAscParams,
) ([]model.Run, error) {
	switch queryRunsSortKey(col, dir) {
	case "created_at_asc":
		return finishQueryRuns(q.QueryRunsCreatedAtAsc(ctx, params))
	case "created_at_desc":
		rows, err := q.QueryRunsCreatedAtDesc(ctx, sqlcdb.QueryRunsCreatedAtDescParams(params))
		return finishQueryRuns(castQueryRunsRows(rows, func(r sqlcdb.QueryRunsCreatedAtDescRow) sqlcdb.QueryRunsCreatedAtAscRow {
			return sqlcdb.QueryRunsCreatedAtAscRow(r)
		}), err)
	case "start_at_asc":
		rows, err := q.QueryRunsStartAtAsc(ctx, sqlcdb.QueryRunsStartAtAscParams(params))
		return finishQueryRuns(castQueryRunsRows(rows, func(r sqlcdb.QueryRunsStartAtAscRow) sqlcdb.QueryRunsCreatedAtAscRow {
			return sqlcdb.QueryRunsCreatedAtAscRow(r)
		}), err)
	case "start_at_desc":
		rows, err := q.QueryRunsStartAtDesc(ctx, sqlcdb.QueryRunsStartAtDescParams(params))
		return finishQueryRuns(castQueryRunsRows(rows, func(r sqlcdb.QueryRunsStartAtDescRow) sqlcdb.QueryRunsCreatedAtAscRow {
			return sqlcdb.QueryRunsCreatedAtAscRow(r)
		}), err)
	case "task_name_asc":
		rows, err := q.QueryRunsTaskNameAsc(ctx, sqlcdb.QueryRunsTaskNameAscParams(params))
		return finishQueryRuns(castQueryRunsRows(rows, func(r sqlcdb.QueryRunsTaskNameAscRow) sqlcdb.QueryRunsCreatedAtAscRow {
			return sqlcdb.QueryRunsCreatedAtAscRow(r)
		}), err)
	case "task_name_desc":
		rows, err := q.QueryRunsTaskNameDesc(ctx, sqlcdb.QueryRunsTaskNameDescParams(params))
		return finishQueryRuns(castQueryRunsRows(rows, func(r sqlcdb.QueryRunsTaskNameDescRow) sqlcdb.QueryRunsCreatedAtAscRow {
			return sqlcdb.QueryRunsCreatedAtAscRow(r)
		}), err)
	case "status_asc":
		rows, err := q.QueryRunsStatusAsc(ctx, sqlcdb.QueryRunsStatusAscParams(params))
		return finishQueryRuns(castQueryRunsRows(rows, func(r sqlcdb.QueryRunsStatusAscRow) sqlcdb.QueryRunsCreatedAtAscRow {
			return sqlcdb.QueryRunsCreatedAtAscRow(r)
		}), err)
	case "status_desc":
		rows, err := q.QueryRunsStatusDesc(ctx, sqlcdb.QueryRunsStatusDescParams(params))
		return finishQueryRuns(castQueryRunsRows(rows, func(r sqlcdb.QueryRunsStatusDescRow) sqlcdb.QueryRunsCreatedAtAscRow {
			return sqlcdb.QueryRunsCreatedAtAscRow(r)
		}), err)
	case "exit_code_asc":
		rows, err := q.QueryRunsExitCodeAsc(ctx, sqlcdb.QueryRunsExitCodeAscParams(params))
		return finishQueryRuns(castQueryRunsRows(rows, func(r sqlcdb.QueryRunsExitCodeAscRow) sqlcdb.QueryRunsCreatedAtAscRow {
			return sqlcdb.QueryRunsCreatedAtAscRow(r)
		}), err)
	case "exit_code_desc":
		rows, err := q.QueryRunsExitCodeDesc(ctx, sqlcdb.QueryRunsExitCodeDescParams(params))
		return finishQueryRuns(castQueryRunsRows(rows, func(r sqlcdb.QueryRunsExitCodeDescRow) sqlcdb.QueryRunsCreatedAtAscRow {
			return sqlcdb.QueryRunsCreatedAtAscRow(r)
		}), err)
	case "duration_asc":
		rows, err := q.QueryRunsDurationAsc(ctx, sqlcdb.QueryRunsDurationAscParams(params))
		return finishQueryRuns(castQueryRunsRows(rows, func(r sqlcdb.QueryRunsDurationAscRow) sqlcdb.QueryRunsCreatedAtAscRow {
			return sqlcdb.QueryRunsCreatedAtAscRow(r)
		}), err)
	case "duration_desc":
		rows, err := q.QueryRunsDurationDesc(ctx, sqlcdb.QueryRunsDurationDescParams(params))
		return finishQueryRuns(castQueryRunsRows(rows, func(r sqlcdb.QueryRunsDurationDescRow) sqlcdb.QueryRunsCreatedAtAscRow {
			return sqlcdb.QueryRunsCreatedAtAscRow(r)
		}), err)
	}
	// Defensive: ParseSortColumn rejects unknown columns before they reach
	// this dispatcher; the error path exists so a future caller who skips
	// the parser sees a clear failure rather than silent default behaviour.
	return nil, fmt.Errorf("unknown sort column %q", col)
}

// castQueryRunsRows applies a per-element conversion to a sqlc row slice,
// projecting every variant onto the shared QueryRunsCreatedAtAscRow type.
// Each callsite passes a Go struct conversion, which compiles to a copy with
// no field-by-field work — the 12 row types share underlying layout.
func castQueryRunsRows[From any](rows []From, conv func(From) sqlcdb.QueryRunsCreatedAtAscRow) []sqlcdb.QueryRunsCreatedAtAscRow {
	out := make([]sqlcdb.QueryRunsCreatedAtAscRow, len(rows))
	for i, r := range rows {
		out[i] = conv(r)
	}
	return out
}

// finishQueryRuns turns a sqlc row slice and the call's error into the
// domain row type. Centralising the loop and the model mapping keeps the
// dispatcher shallow even with 12 sort variants.
func finishQueryRuns(rows []sqlcdb.QueryRunsCreatedAtAscRow, err error) ([]model.Run, error) {
	if err != nil {
		return nil, err
	}
	out := make([]model.Run, len(rows))
	for i, r := range rows {
		out[i] = model.Run{
			ID:                  r.ID,
			ExternalExecutionID: r.ExternalExecutionID,
			TaskName:            r.TaskName,
			Status:              r.Status,
			EndReason:           r.EndReason,
			ExitCode:            r.ExitCode,
			StartAt:             r.StartAt,
			EndAt:               r.EndAt,
			TriggeredBy:         r.TriggeredBy,
			CreatedAt:           r.CreatedAt,
			RetryAttempt:        r.RetryAttempt,
			RetryOfRunID:        r.RetryOfRunID,
			InstanceIndex:       r.InstanceIndex,
			Params:              decodeParams(r.ParamsJson, r.ID),
		}
	}
	return out, nil
}
