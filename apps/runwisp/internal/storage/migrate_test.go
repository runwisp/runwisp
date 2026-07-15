// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"database/sql"
	"testing"

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

// TestRunMigrations_AdoptsPreexistingDB simulates a database created before the
// migration system existed: the full baseline schema is already present but
// user_version is still 0. The IF NOT EXISTS baseline must adopt it without
// error and stamp it to head.
func TestRunMigrations_AdoptsPreexistingDB(t *testing.T) {
	db := openRaw(t)

	// Materialize the baseline objects, then reset user_version to 0 to mimic a
	// DB that predates the migration system.
	require.NoError(t, runMigrations(db))
	_, err := db.Exec("PRAGMA user_version = 0")
	require.NoError(t, err)

	require.NoError(t, runMigrations(db))
	require.Equal(t, headVersion(t), readUserVersion(t, db))
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
