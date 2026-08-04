// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package storage

import (
	"fmt"

	"github.com/runwisp/runwisp/internal/model"
)

// RunQuery groups the orthogonal axes of a runs-list query — filter,
// pagination, sort — into a single value. QueryRuns takes this struct so
// its signature stays short as new query knobs land.
type RunQuery struct {
	Filter        model.RunFilter
	Limit, Offset int
	SortField     SortColumn
	SortDirection SortDirection
}

// SortColumn is a typed handle for a column the runs list may be sorted by.
// Callers receive the typed value via ParseSortColumn so unknown strings are
// rejected at the API boundary; the storage layer never sees raw column names.
type SortColumn string

const (
	SortColumnDefault   SortColumn = ""
	SortColumnCreatedAt SortColumn = "createdAt"
	SortColumnStartAt   SortColumn = "startAt"
	SortColumnTaskName  SortColumn = "taskName"
	SortColumnStatus    SortColumn = "status"
	SortColumnExitCode  SortColumn = "exitCode"
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

// allowedSortColumns is the set of SortColumn values ParseSortColumn accepts.
// QueryRuns dispatches each (col, dir) pair to its dedicated sqlc query, so
// no SQL fragment lives here — the map is the source of truth for "is this
// a known column?".
var allowedSortColumns = map[SortColumn]struct{}{
	SortColumnCreatedAt: {},
	SortColumnStartAt:   {},
	SortColumnTaskName:  {},
	SortColumnStatus:    {},
	SortColumnExitCode:  {},
	SortColumnDuration:  {},
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
