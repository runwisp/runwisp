// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package storage

import (
	"context"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/storage/sqlcdb"
)

// grepRunsPage is ListGrepRuns' LIMIT.
const grepRunsPage = 500

// storedTimeLayout is the text _time_format=sqlite writes a time.Time as (see
// New). Every stored time is UTC, so in this layout text order is time order.
const storedTimeLayout = "2006-01-02 15:04:05.999999999-07:00"

// GrepRunsQuery selects the runs log:grep scans: station executions (an
// execution id, not deleted) that started at or after Since and before
// Before, of the tasks named in TaskNames. A run that never started counts
// from its creation. Zero times and an empty TaskNames leave that side open.
type GrepRunsQuery struct {
	Since     time.Time
	Before    time.Time
	TaskNames []string
}

// GrepRun is one run log:grep scans.
type GrepRun struct {
	ID          string
	ExecutionID string
	TaskName    string
	CreatedAt   time.Time
}

// GrepRuns calls yield with every run q selects, newest start first, until
// yield returns false. It reads idx_runs_grep one keyset page at a time, so
// each query is short (the daemon has one SQLite connection) and memory is
// one page whatever the size of the table.
func (db *SQLiteDatabase) GrepRuns(ctx context.Context, q GrepRunsQuery, yield func(GrepRun) bool) error {
	before, since := q.bounds()
	beforeID := ""
	p := sqlcdb.ListGrepRunsParams{BeforeStart: &before, BeforeID: &beforeID, Since: &since, AllTasks: len(q.TaskNames) == 0, TaskNames: q.TaskNames}
	for {
		page, err := db.q.ListGrepRuns(ctx, p)
		if err != nil {
			return err
		}
		for _, r := range page {
			if !yield(GrepRun{ID: r.ID, ExecutionID: *r.ExecutionID, TaskName: r.TaskName, CreatedAt: r.CreatedAt}) {
				return nil
			}
		}
		if len(page) < grepRunsPage {
			return nil
		}
		last := page[len(page)-1]
		p.BeforeStart, p.BeforeID = &last.SortKey, &last.ID
	}
}

// CountGrepRuns counts the runs GrepRuns yields for q. It reads the index
// entries in q's time range only, never the rows.
func (db *SQLiteDatabase) CountGrepRuns(ctx context.Context, q GrepRunsQuery) (int, error) {
	before, since := q.bounds()
	n, err := db.q.CountGrepRuns(ctx, sqlcdb.CountGrepRunsParams{
		BeforeStart: &before, Since: &since, AllTasks: len(q.TaskNames) == 0, TaskNames: q.TaskNames,
	})
	return int(n), err
}

// bounds renders q's time range as stored text: "9" sorts after every stored
// time and "" before every one.
func (q GrepRunsQuery) bounds() (before, since string) {
	before = "9"
	if !q.Before.IsZero() {
		before = q.Before.UTC().Format(storedTimeLayout)
	}
	if !q.Since.IsZero() {
		since = q.Since.UTC().Format(storedTimeLayout)
	}
	return before, since
}
