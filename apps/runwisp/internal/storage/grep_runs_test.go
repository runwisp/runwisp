// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/storage/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// grepFillStart is when the synthetic runs start, one every 2.6 s: a million
// runs a month.
var grepFillStart = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// fillGrepRuns inserts n runs. Run i is task-(i%50), except every 100000th
// (offset 7), which is "rare"; every tenth has no execution id and every
// 97th is deleted. Run 3 never started, so it sorts by created_at.
func fillGrepRuns(t testing.TB, db *SQLiteDatabase, n int) {
	t.Helper()
	_, err := db.db.Exec(`INSERT INTO runs (id, execution_id, task_name, status, triggered_by, created_at, started_at, deleted_at)
WITH RECURSIVE seq(i) AS (SELECT 0 UNION ALL SELECT i + 1 FROM seq WHERE i < ? - 1)
SELECT printf('%026d', i),
  CASE WHEN i % 10 != 9 THEN 'exec-' || i END,
  CASE WHEN i % 100000 = 7 THEN 'rare' ELSE 'task-' || (i % 50) END,
  'ended', 'schedule',
  strftime('%Y-%m-%d %H:%M:%f', ? + i * 2.6, 'unixepoch') || '+00:00',
  CASE WHEN i != 3 THEN strftime('%Y-%m-%d %H:%M:%f', ? + i * 2.6 + 0.5, 'unixepoch') || '+00:00' END,
  CASE WHEN i % 97 = 0 THEN '2026-06-01 00:00:00+00:00' END
FROM seq`, n, grepFillStart.Unix(), grepFillStart.Unix())
	require.NoError(t, err)
}

// grepFillAt is when synthetic run i started.
func grepFillAt(i int) time.Time {
	return grepFillStart.Add(time.Duration(i)*2600*time.Millisecond + 500*time.Millisecond)
}

func collectGrepRuns(t testing.TB, db *SQLiteDatabase, q GrepRunsQuery) []GrepRun {
	t.Helper()
	var out []GrepRun
	require.NoError(t, db.GrepRuns(t.Context(), q, func(r GrepRun) bool {
		out = append(out, r)
		return true
	}))
	return out
}

func TestGrepRuns_PagesNewestFirstWithFilters(t *testing.T) {
	db, err := New(":memory:")
	require.NoError(t, err)
	defer db.Close()
	fillGrepRuns(t, db, 2000)

	all := collectGrepRuns(t, db, GrepRunsQuery{})
	// 2000 runs, less 200 local ones and 19 more deleted ones.
	require.Len(t, all, 1781)
	for i := 1; i < len(all); i++ {
		require.Greater(t, all[i-1].ID, all[i].ID, "newest first, no run twice")
	}
	assert.Equal(t, "exec-1998", all[0].ExecutionID)
	assert.Equal(t, "task-48", all[0].TaskName)
	assert.True(t, grepFillStart.Add(1998*2600*time.Millisecond).Equal(all[0].CreatedAt))
	n, err := db.CountGrepRuns(t.Context(), GrepRunsQuery{})
	require.NoError(t, err)
	assert.Equal(t, len(all), n)

	// Runs 1000-1099: 1000 starts exactly at Since, 1100 at Before.
	q := GrepRunsQuery{Since: grepFillAt(1000), Before: grepFillAt(1100), TaskNames: []string{"task-0", "task-1", "task-17", "task-49"}}
	got := collectGrepRuns(t, db, q)
	var ids []string
	for _, r := range got {
		ids = append(ids, strings.TrimLeft(r.ID, "0"))
	}
	assert.Equal(t, []string{"1051", "1050", "1017", "1001", "1000"}, ids, "task-17's 1067 is deleted, task-49's 1049 and 1099 are local")
	n, err = db.CountGrepRuns(t.Context(), q)
	require.NoError(t, err)
	assert.Equal(t, 5, n)

	// Run 3 never started: it is placed by created_at, after run 2.
	got = collectGrepRuns(t, db, GrepRunsQuery{Before: grepFillAt(4), TaskNames: []string{"task-3", "task-2"}})
	require.Len(t, got, 2)
	assert.Equal(t, []string{"task-3", "task-2"}, []string{got[0].TaskName, got[1].TaskName})

	stopped := 0
	require.NoError(t, db.GrepRuns(t.Context(), GrepRunsQuery{}, func(GrepRun) bool { stopped++; return stopped < 3 }))
	assert.Equal(t, 3, stopped)
}

// recordingDB records the queries sqlc sends instead of running them.
type recordingDB struct {
	sqlcdb.DBTX
	query string
	args  []any
}

func (r *recordingDB) QueryContext(_ context.Context, query string, args ...any) (*sql.Rows, error) {
	r.query, r.args = query, args
	return nil, sql.ErrNoRows
}

func (r *recordingDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	r.query, r.args = query, args
	return r.DBTX.QueryRowContext(ctx, "SELECT 1 WHERE 0")
}

// grepRunsPlans returns the query plan of every grep query shape: one page and
// the count, each with and without a task filter.
func grepRunsPlans(t testing.TB, db *SQLiteDatabase) map[string]string {
	t.Helper()
	rec := &recordingDB{DBTX: db.db}
	q := sqlcdb.New(rec)
	plans := map[string]string{}
	explain := func(name string) {
		rows, err := db.db.Query("EXPLAIN QUERY PLAN "+rec.query, rec.args...)
		require.NoError(t, err)
		defer rows.Close()
		var steps []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			require.NoError(t, rows.Scan(&id, &parent, &unused, &detail))
			steps = append(steps, detail)
		}
		require.NoError(t, rows.Err())
		plans[name] = strings.Join(steps, "; ")
	}
	for _, tasks := range [][]string{nil, {"task-1", "station-task-1"}} {
		suffix := map[bool]string{true: " all tasks", false: " some tasks"}[tasks == nil]
		_, _ = q.ListGrepRuns(t.Context(), sqlcdb.ListGrepRunsParams{BeforeStart: new("9"), BeforeID: new(""), Since: new(""), AllTasks: tasks == nil, TaskNames: tasks})
		explain("page" + suffix)
		_, _ = q.CountGrepRuns(t.Context(), sqlcdb.CountGrepRunsParams{BeforeStart: new("9"), Since: new(""), AllTasks: tasks == nil, TaskNames: tasks})
		explain("count" + suffix)
	}
	return plans
}

// Every grep query walks idx_runs_grep in order: no table scan, no sort.
func TestGrepRuns_QueriesUseTheIndex(t *testing.T) {
	db, err := New(":memory:")
	require.NoError(t, err)
	defer db.Close()
	fillGrepRuns(t, db, 1000)
	for name, plan := range grepRunsPlans(t, db) {
		assert.Contains(t, plan, "SEARCH runs USING INDEX idx_runs_grep (<expr>>? AND <expr><?)", name)
		assert.NotContains(t, plan, "TEMP B-TREE", name)
	}
}

// BenchmarkGrepRuns pages through a million runs (a month at one run every
// 2.6 s) the way log:grep does.
func BenchmarkGrepRuns(b *testing.B) {
	db, err := New(filepath.Join(b.TempDir(), "runs.db"))
	require.NoError(b, err)
	defer db.Close()
	fillGrepRuns(b, db, 1_000_000)
	for name, plan := range grepRunsPlans(b, db) {
		b.Logf("%s: %s", name, plan)
	}
	lastDay := grepFillAt(1_000_000).Add(-24 * time.Hour)
	for _, bc := range []struct {
		name string
		q    GrepRunsQuery
	}{
		{"all", GrepRunsQuery{}},
		{"last day", GrepRunsQuery{Since: lastDay}},
		{"one task", GrepRunsQuery{TaskNames: []string{"task-1", "station-task-1"}}},
		{"rare task", GrepRunsQuery{TaskNames: []string{"rare", "station-rare"}}},
	} {
		b.Run("list "+bc.name, func(b *testing.B) {
			for b.Loop() {
				collectGrepRuns(b, db, bc.q)
			}
		})
		b.Run("first page "+bc.name, func(b *testing.B) {
			for b.Loop() {
				n := 0
				require.NoError(b, db.GrepRuns(b.Context(), bc.q, func(GrepRun) bool { n++; return n < grepRunsPage }))
			}
		})
		b.Run("count "+bc.name, func(b *testing.B) {
			for b.Loop() {
				_, err := db.CountGrepRuns(b.Context(), bc.q)
				require.NoError(b, err)
			}
		})
	}
}
