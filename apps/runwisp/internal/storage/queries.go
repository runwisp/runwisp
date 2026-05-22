// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import "fmt"

// SortColumn is a typed handle for a column the runs list may be sorted by.
// Callers receive the typed value via ParseSortColumn so unknown strings are
// rejected at the API boundary; the storage layer never sees raw column names.
type SortColumn string

const (
	SortColumnDefault   SortColumn = ""
	SortColumnCreatedAt SortColumn = "created_at"
	SortColumnStartAt   SortColumn = "start_at"
	SortColumnTaskName  SortColumn = "task_name"
	SortColumnStatus    SortColumn = "status"
	SortColumnExitCode  SortColumn = "exit_code"
	SortColumnDuration  SortColumn = "duration"
)

// SortDirection is the typed handle for ASC/DESC. Empty means "default for
// the chosen column", which today is always DESC.
type SortDirection string

const (
	SortDirectionDefault SortDirection = ""
	SortAsc              SortDirection = "asc"
	SortDesc             SortDirection = "desc"
)

// allowedSortColumns maps an accepted SortColumn to the ORDER BY fragment
// (without the direction) that should be emitted. Columns that don't appear
// here are rejected by ParseSortColumn.
var allowedSortColumns = map[SortColumn]string{
	SortColumnCreatedAt: "created_at",
	SortColumnStartAt:   "start_at",
	SortColumnTaskName:  "task_name",
	SortColumnStatus:    "status",
	SortColumnExitCode:  "exit_code",
	SortColumnDuration:  "duration",
}

// ParseSortColumn validates a raw HTTP/CLI sort key against the allow-list.
// An empty input yields SortColumnDefault (caller decides the fallback).
func ParseSortColumn(raw string) (SortColumn, error) {
	if raw == "" {
		return SortColumnDefault, nil
	}
	col := SortColumn(raw)
	if _, ok := allowedSortColumns[col]; !ok {
		return SortColumnDefault, fmt.Errorf("unknown sort column %q", raw)
	}
	return col, nil
}

// ParseSortDirection validates a raw direction. Empty is accepted and means
// "use the column's default".
func ParseSortDirection(raw string) (SortDirection, error) {
	switch SortDirection(raw) {
	case SortDirectionDefault:
		return SortDirectionDefault, nil
	case SortAsc:
		return SortAsc, nil
	case SortDesc:
		return SortDesc, nil
	default:
		return SortDirectionDefault, fmt.Errorf("unknown sort direction %q", raw)
	}
}

// directionSQL returns the literal SQL keyword for a SortDirection, defaulting
// to DESC when the caller didn't pick one.
func (d SortDirection) directionSQL() string {
	if d == SortAsc {
		return "ASC"
	}
	return "DESC"
}

// buildOrderClause renders the ORDER BY tail for QueryRuns. The column has
// already been validated by ParseSortColumn, so this cannot fail; we still
// return an error for forward-compatibility and to make the call site obvious.
func buildOrderClause(col SortColumn, dir SortDirection) (string, error) {
	if col == SortColumnDefault {
		return "created_at DESC", nil
	}
	direction := dir.directionSQL()
	switch col {
	case SortColumnDuration:
		return fmt.Sprintf("(COALESCE(julianday(end_at) - julianday(start_at), 0)) %s", direction), nil
	case SortColumnStartAt:
		return fmt.Sprintf("COALESCE(start_at, created_at) %s, created_at %s", direction, direction), nil
	}
	sqlCol, ok := allowedSortColumns[col]
	if !ok {
		// Defensive: ParseSortColumn should have caught this; treat as bug.
		return "", fmt.Errorf("unknown sort column %q", col)
	}
	return fmt.Sprintf("%s %s", sqlCol, direction), nil
}

// notDeletedClause filters out soft-deleted rows. Appended to every read path
// so deleted rows are invisible to the UI until the purger reaps them.
const notDeletedClause = "deleted_at IS NULL"

// runColumns is the canonical ordered column list for full-row reads from
// runs. Kept here next to its only consumer (selectFromRuns) — the rest of
// the daemon either uses sqlc-generated scans or relies on the SELECT *
// equivalent via selectFromRuns.
const runColumns = `id, external_execution_id, task_name, status, end_reason, exit_code,
start_at, end_at, triggered_by, created_at, retry_attempt, retry_of_run_id, instance_index`

// selectFromRuns is the common SELECT runColumns FROM runs prefix used by
// every full-row read. Hoisting it keeps the static query constants from
// duplicating either the column list or the SELECT keyword (S1192).
const selectFromRuns = `SELECT ` + runColumns + ` FROM runs`

// filterGatesSQL is the static filter-gates predicate block appended to
// every selector-driven query that takes a RunFilter. Each gate has the
// shape "(? = empty OR col = ?)": an empty gate value disables the filter,
// a non-empty one matches the column. The search gate takes three params
// (gate + two LIKE patterns). See runFilterGateArgs for the parameter
// layout — the SQL surface is fixed at compile time, only values are bound.
const filterGatesSQL = `
  AND (? = '' OR end_reason = ?)
  AND (? = '' OR status = ?)
  AND (? = '' OR task_name = ?)
  AND (? = '' OR (task_name LIKE ? OR id LIKE ?))`

// inIDsSQL / notInIDsSQL accept a JSON-array string via json_each(?) so the
// SQL is static even though the ID cardinality is variable. See idsJSON.
const inIDsSQL = `
  AND id IN (SELECT value FROM json_each(?))`
const notInIDsSQL = `
  AND id NOT IN (SELECT value FROM json_each(?))`

// statusFilterGateSQL is the trailing optional bulk-status gate used by
// ResolveSelectorIDs to narrow MatchAll selections.
const statusFilterGateSQL = `
  AND (? = '' OR status = ?)`

// CountRunsFiltered query.
const countRunsFilteredSQL = `SELECT COUNT(*) FROM runs WHERE deleted_at IS NULL` + filterGatesSQL

// getRunSummarySQL is hand-written instead of sqlc-generated because
// last_failure is a scalar MAX(end_at) subquery — sqlc cannot infer the
// column type through the aggregate and would emit interface{}, forcing a
// runtime type assertion at the call site.
const getRunSummarySQL = `SELECT
  CAST(COUNT(*) AS INTEGER) AS total,
  CAST(COALESCE(SUM(CASE WHEN end_reason = 'success' THEN 1 ELSE 0 END), 0) AS INTEGER) AS success,
  CAST(COALESCE(SUM(CASE WHEN end_reason IN ('failed','crashed','timeout','log_overflow')
                         THEN 1 ELSE 0 END), 0) AS INTEGER) AS failed,
  (SELECT MAX(end_at) FROM runs
   WHERE end_reason IN ('failed','crashed','timeout','log_overflow')
     AND deleted_at IS NULL) AS last_failure
FROM runs WHERE deleted_at IS NULL`

// SoftDeleteRuns — two static shapes (by ID list vs by filter).
// status = ? is bound to PhaseEnded; only terminal runs can be soft-deleted.
const softDeleteByIDsSQL = `UPDATE runs SET deleted_at = ?
WHERE deleted_at IS NULL
  AND status = ?` + inIDsSQL + `
RETURNING id, task_name, created_at`

const softDeleteByFilterSQL = `UPDATE runs SET deleted_at = ?
WHERE deleted_at IS NULL
  AND status = ?` + filterGatesSQL + notInIDsSQL + `
RETURNING id, task_name, created_at`

// RestoreRuns — clears deleted_at, then re-reads the rows to publish events.
const restoreByIDsSQL = `UPDATE runs SET deleted_at = NULL
WHERE deleted_at IS NOT NULL` + inIDsSQL

const restoreByFilterSQL = `UPDATE runs SET deleted_at = NULL
WHERE deleted_at IS NOT NULL` + filterGatesSQL + notInIDsSQL

const selectRestoredByIDsSQL = selectFromRuns + `
WHERE deleted_at IS NULL` + inIDsSQL

const selectRestoredByFilterSQL = selectFromRuns + `
WHERE deleted_at IS NULL` + filterGatesSQL + notInIDsSQL

// ResolveSelectorIDs — same selector shapes plus the trailing bulk-status gate.
const resolveByIDsSQL = `SELECT id, task_name, created_at FROM runs
WHERE deleted_at IS NULL` + inIDsSQL + statusFilterGateSQL

const resolveByFilterSQL = `SELECT id, task_name, created_at FROM runs
WHERE deleted_at IS NULL` + filterGatesSQL + notInIDsSQL + statusFilterGateSQL

// selectRunsSQL composes a `SELECT runColumns FROM runs <where> <tail>` query,
// implicitly adding the soft-delete filter to whatever predicates the caller
// passed in. tail is appended verbatim — callers use it for ORDER BY / LIMIT.
//
// Used by the legacy dynamic-query paths (QueryRuns, GetPendingRuns,
// GetLastRunByTask, DeleteOldRuns) whose ORDER BY tails are still assembled
// at runtime from validated, hardcoded fragments.
func selectRunsSQL(where, tail string) string {
	q := selectFromRuns
	combined := combineWhere(where, notDeletedClause)
	if combined != "" {
		q += " " + combined
	}
	if tail != "" {
		q += " " + tail
	}
	return q
}

// combineWhere appends an extra predicate to an optional caller-supplied
// WHERE clause. The empty string means "no caller predicate" — the helper
// then becomes the sole `WHERE extra` source.
func combineWhere(existing, extra string) string {
	if existing == "" {
		return "WHERE " + extra
	}
	return existing + " AND " + extra
}
