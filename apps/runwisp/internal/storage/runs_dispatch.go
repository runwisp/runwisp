// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package storage

import (
	"context"
	"fmt"
	"time"

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

// queryRunsRow is the row type of every QueryRuns variant: they share one
// SELECT list, so sqlc emits structurally identical row structs. Keep in
// sync with sqlcdb.QueryRunsCreatedAtAscRow (the compiler flags drift).
type queryRunsRow interface {
	~struct {
		ID            string            `json:"id"`
		ExecutionID   *string           `json:"execution_id"`
		TaskName      string            `json:"task_name"`
		Status        model.RunPhase    `json:"status"`
		EndReason     *model.EndReason  `json:"end_reason"`
		ExitCode      int               `json:"exit_code"`
		StartedAt     *time.Time        `json:"started_at"`
		EndedAt       *time.Time        `json:"ended_at"`
		TriggeredBy   model.TriggeredBy `json:"triggered_by"`
		CreatedAt     time.Time         `json:"created_at"`
		RetryAttempt  int               `json:"retry_attempt"`
		RetryOfRunID  *string           `json:"retry_of_run_id"`
		InstanceIndex int               `json:"instance_index"`
		ParamsJson    *string           `json:"params_json"` //nolint:revive // must match sqlc's generated field name
		IsFailure     int64             `json:"is_failure"`
	}
}

// finishQueryRuns turns a sqlc row slice and the call's error into the
// domain row type.
func finishQueryRuns[R queryRunsRow](rows []R, err error) ([]model.Run, error) {
	if err != nil {
		return nil, err
	}
	out := make([]model.Run, len(rows))
	for i, row := range rows {
		r := sqlcdb.QueryRunsCreatedAtAscRow(row)
		out[i] = model.Run{
			ID:            r.ID,
			ExecutionID:   r.ExecutionID,
			TaskName:      r.TaskName,
			Status:        r.Status,
			EndReason:     r.EndReason,
			ExitCode:      r.ExitCode,
			StartedAt:     r.StartedAt,
			EndedAt:       r.EndedAt,
			TriggeredBy:   r.TriggeredBy,
			CreatedAt:     r.CreatedAt,
			RetryAttempt:  r.RetryAttempt,
			RetryOfRunID:  r.RetryOfRunID,
			InstanceIndex: r.InstanceIndex,
			IsFailure:     r.IsFailure != 0,
			Params:        decodeParams(r.ParamsJson, r.ID),
		}
	}
	return out, nil
}
