// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

// migrationsFS holds the forward-only schema migrations, applied once each in
// ascending version order. Files are named "<version>_<description>.sql" with a
// zero-padded 4-digit version prefix (e.g. "0002_add_run_labels.sql") so lexical
// order matches numeric order. This directory is also the sqlc codegen input
// (see sqlc.yaml), so runtime schema and generated code cannot drift.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// migration is a single forward-only step parsed from migrationsFS.
type migration struct {
	version int
	name    string
	sql     string
}

// runMigrations applies every migration whose version is greater than the
// database's current PRAGMA user_version, in ascending order, each in its own
// transaction. On any error the transaction rolls back and the database is left
// at the previously applied version. Migrations are forward-only: there is no
// down path.
func runMigrations(db *sql.DB) error {
	current, err := userVersion(db)
	if err != nil {
		return err
	}

	migrations, err := loadMigrations()
	if err != nil {
		return err
	}

	// A database stamped newer than any migration this binary knows was written
	// by a newer runwisp. Migrations are forward-only, so we cannot bring it back
	// down, and reading it could misinterpret schema this binary predates. Fail
	// loudly instead of silently no-opping (the loop below would skip every
	// migration and return nil) — the operator must upgrade the binary.
	maxKnown := 0
	for _, m := range migrations {
		if m.version > maxKnown {
			maxKnown = m.version
		}
	}
	if current > maxKnown {
		return fmt.Errorf("database schema version %d is newer than this binary supports (max %d); upgrade runwisp to open this data directory", current, maxKnown)
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := applyMigration(db, m); err != nil {
			return fmt.Errorf("apply migration %s: %w", m.name, err)
		}
	}
	return nil
}

// applyMigration executes one migration and stamps user_version, atomically.
func applyMigration(db *sql.DB, m migration) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(m.sql); err != nil {
		_ = tx.Rollback()
		return err
	}
	// PRAGMA user_version does not accept bound parameters, so the value is
	// interpolated. m.version is an int parsed from an embedded filename, never
	// user input — no injection surface.
	if _, err := tx.Exec("PRAGMA user_version = " + strconv.Itoa(m.version)); err != nil { //NOSONAR: go:S2077 — m.version is an int from an embedded filename; PRAGMA cannot be parameterized
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func userVersion(db *sql.DB) (int, error) {
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		return 0, fmt.Errorf("read user_version: %w", err)
	}
	return v, nil
}

// loadMigrations reads and parses every embedded migration.
func loadMigrations() ([]migration, error) {
	return parseMigrations(migrationsFS)
}

// parseMigrations reads and parses every migration under the "migrations"
// directory of fsys, sorted ascending by version. It errors on a malformed
// filename or duplicate version so a mis-added file fails loudly at startup
// rather than silently reordering. Split from loadMigrations so tests can feed
// a synthetic filesystem.
func parseMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}

	migrations := make([]migration, 0, len(entries))
	seen := make(map[int]string, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		version, err := parseMigrationVersion(e.Name())
		if err != nil {
			return nil, err
		}
		if other, dup := seen[version]; dup {
			return nil, fmt.Errorf("duplicate migration version %d: %s and %s", version, other, e.Name())
		}
		seen[version] = e.Name()

		body, err := fs.ReadFile(fsys, "migrations/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		migrations = append(migrations, migration{version: version, name: e.Name(), sql: string(body)})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})
	return migrations, nil
}

// parseMigrationVersion extracts the leading integer of a migration filename,
// e.g. "0002_add_run_labels.sql" -> 2. The version must be positive.
func parseMigrationVersion(name string) (int, error) {
	prefix, _, found := strings.Cut(name, "_")
	if !found {
		return 0, fmt.Errorf("migration %q must be named <version>_<description>.sql", name)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, fmt.Errorf("migration %q has a non-numeric version prefix: %w", name, err)
	}
	if version <= 0 {
		return 0, fmt.Errorf("migration %q must have a positive version prefix", name)
	}
	return version, nil
}
