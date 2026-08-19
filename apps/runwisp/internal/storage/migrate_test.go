// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package storage

import (
	"database/sql"
	"strconv"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

// openRaw returns a single-connection in-memory database. SetMaxOpenConns(1)
// keeps every query on the same connection so the :memory: schema persists
// across calls within a test (a second connection would be a fresh empty DB).
func openRaw(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func headVersion(t *testing.T) int {
	t.Helper()
	migs, err := loadMigrations()
	require.NoError(t, err)
	require.NotEmpty(t, migs, "expected at least one embedded migration")
	return migs[len(migs)-1].version
}

func readUserVersion(t *testing.T, db *sql.DB) int {
	t.Helper()
	v, err := userVersion(db)
	require.NoError(t, err)
	return v
}

func TestNew_MigratesToHead(t *testing.T) {
	db, err := New(":memory:")
	require.NoError(t, err)
	defer db.Close()

	sdb, ok := db.(*SQLiteDatabase)
	require.True(t, ok)

	require.Equal(t, headVersion(t), readUserVersion(t, sdb.db))
}

func TestRunMigrations_IsIdempotent(t *testing.T) {
	db := openRaw(t)

	require.NoError(t, runMigrations(db))
	first := readUserVersion(t, db)
	require.Equal(t, headVersion(t), first)

	// A second pass must apply nothing and leave the version untouched.
	require.NoError(t, runMigrations(db))
	require.Equal(t, first, readUserVersion(t, db))
}

// TestRunMigrations_RejectsNewerSchema is the regression test for Bug D: a
// database stamped with a user_version above every migration this binary knows
// was written by a newer runwisp. Migrations are forward-only, so the runner
// must fail loudly (the operator has to upgrade the binary) rather than silently
// no-op every migration and return nil against schema it may misread.
func TestRunMigrations_RejectsNewerSchema(t *testing.T) {
	db := openRaw(t)
	require.NoError(t, runMigrations(db))

	// Stamp the DB one past head, as a future binary's migration would.
	_, err := db.Exec("PRAGMA user_version = " + strconv.Itoa(headVersion(t)+1))
	require.NoError(t, err)

	err = runMigrations(db)
	require.Error(t, err, "a DB newer than the binary supports must be rejected, not silently opened")
	require.Contains(t, err.Error(), "newer than this binary supports")
}

// TestRunMigrations_AdoptsPreexistingDB simulates a database created before the
// migration system existed: the full baseline schema is already present but
// user_version is still 0. The IF NOT EXISTS baseline must adopt it without
// error and stamp it to head.
func TestRunMigrations_AdoptsPreexistingDB(t *testing.T) {
	db := openRaw(t)

	// A DB that predates the migration system has the baseline (v1) schema in
	// place but user_version still 0. Materialize ONLY that baseline — not the
	// full chain — so the forward migrations still see their pre-rename columns,
	// exactly as they would against a real old database.
	migs, err := loadMigrations()
	require.NoError(t, err)
	require.NoError(t, applyMigration(db, migs[0]))
	_, err = db.Exec("PRAGMA user_version = 0")
	require.NoError(t, err)

	require.NoError(t, runMigrations(db))
	require.Equal(t, headVersion(t), readUserVersion(t, db))
}

// TestMigration0002_RenamesTimestampColumns applies only the baseline, writes
// rows under the pre-rename column names, then applies 0002 and asserts the
// columns were renamed and every value survived — a RENAME COLUMN must carry the
// data, not drop it.
func TestMigration0002_RenamesTimestampColumns(t *testing.T) {
	db := openRaw(t)

	migs, err := loadMigrations()
	require.NoError(t, err)
	byVersion := make(map[int]migration, len(migs))
	for _, m := range migs {
		byVersion[m.version] = m
	}
	v1, ok := byVersion[1]
	require.True(t, ok, "baseline migration must exist")
	v2, ok := byVersion[2]
	require.True(t, ok, "0002 migration must exist")

	// Baseline only: the old start_at/end_at/inserted_at columns are in force.
	require.NoError(t, applyMigration(db, v1))
	_, err = db.Exec(`INSERT INTO runs (id, task_name, status, start_at, end_at, triggered_by, created_at)
		VALUES ('r1', 'backup', 'ended', '2026-01-02T03:04:05Z', '2026-01-02T03:05:00Z', 'schedule', '2026-01-02T03:04:00Z')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO pending_log_uploads (execution_id, upload_url, log_path, inserted_at)
		VALUES ('e1', 'https://example.test/u', '/logs/e1', 1735787040)`)
	require.NoError(t, err)

	// Apply the rename.
	require.NoError(t, applyMigration(db, v2))

	// New names resolve and carry the original values.
	var startedAt, endedAt string
	require.NoError(t, db.QueryRow(`SELECT started_at, ended_at FROM runs WHERE id = 'r1'`).Scan(&startedAt, &endedAt))
	require.Equal(t, "2026-01-02T03:04:05Z", startedAt)
	require.Equal(t, "2026-01-02T03:05:00Z", endedAt)

	var insertedAtUnix int64
	require.NoError(t, db.QueryRow(`SELECT inserted_at_unix FROM pending_log_uploads WHERE execution_id = 'e1'`).Scan(&insertedAtUnix))
	require.Equal(t, int64(1735787040), insertedAtUnix)

	// The old column names must be gone.
	require.Error(t,
		db.QueryRow(`SELECT start_at FROM runs WHERE id = 'r1'`).Scan(new(string)),
		"start_at must not survive the rename")
}

func TestLoadMigrations_OrderedAndWellFormed(t *testing.T) {
	migs, err := loadMigrations()
	require.NoError(t, err)
	require.NotEmpty(t, migs)

	prev := 0
	for _, m := range migs {
		require.Greater(t, m.version, prev, "migrations must be strictly ascending with no duplicate versions")
		require.NotEmpty(t, m.sql, "migration %s is empty", m.name)
		prev = m.version
	}
	require.Equal(t, 1, migs[0].version, "the baseline migration must be version 1")
}

// TestApplyMigration_RollsBackOnError verifies a failing migration leaves the
// database at its prior version — the transaction is rolled back, not partially
// applied.
func TestApplyMigration_RollsBackOnError(t *testing.T) {
	db := openRaw(t)

	err := applyMigration(db, migration{version: 99, name: "0099_broken.sql", sql: "THIS IS NOT SQL;"})
	require.Error(t, err)
	require.Equal(t, 0, readUserVersion(t, db), "version must be unchanged after a failed migration")
}

// TestRunMigrations_ErrorsOnClosedDB exercises the error-propagation path when
// the initial user_version read fails.
func TestRunMigrations_ErrorsOnClosedDB(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	require.NoError(t, db.Close())

	require.Error(t, runMigrations(db))
	require.Error(t, applyMigration(db, migration{version: 1, name: "0001_x.sql", sql: "SELECT 1;"}))

	_, err = userVersion(db)
	require.Error(t, err)
}

func TestParseMigrations_Errors(t *testing.T) {
	t.Run("missing dir", func(t *testing.T) {
		_, err := parseMigrations(fstest.MapFS{})
		require.Error(t, err)
	})

	t.Run("duplicate version", func(t *testing.T) {
		fsys := fstest.MapFS{
			"migrations/0001_a.sql": {Data: []byte("SELECT 1;")},
			"migrations/0001_b.sql": {Data: []byte("SELECT 2;")},
		}
		_, err := parseMigrations(fsys)
		require.ErrorContains(t, err, "duplicate migration version")
	})

	t.Run("malformed filename", func(t *testing.T) {
		fsys := fstest.MapFS{
			"migrations/init.sql": {Data: []byte("SELECT 1;")},
		}
		_, err := parseMigrations(fsys)
		require.Error(t, err)
	})

	t.Run("ignores non-sql and dirs", func(t *testing.T) {
		fsys := fstest.MapFS{
			"migrations/0001_a.sql":   {Data: []byte("SELECT 1;")},
			"migrations/README.md":    {Data: []byte("notes")},
			"migrations/sub/0002.sql": {Data: []byte("SELECT 2;")},
		}
		migs, err := parseMigrations(fsys)
		require.NoError(t, err)
		require.Len(t, migs, 1)
		require.Equal(t, 1, migs[0].version)
	})
}

func TestParseMigrationVersion(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		want    int
		wantErr bool
	}{
		{name: "baseline", file: "0001_initial_schema.sql", want: 1},
		{name: "later", file: "0042_add_labels.sql", want: 42},
		{name: "multi-word description", file: "0002_add_run_labels_column.sql", want: 2},
		{name: "no separator", file: "0001.sql", wantErr: true},
		{name: "non-numeric prefix", file: "init_schema.sql", wantErr: true},
		{name: "zero version", file: "0000_seed.sql", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseMigrationVersion(tt.file)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
