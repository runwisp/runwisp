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
