// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io"
	"os"

	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/runwisp/runwisp/internal/storage/secretcipher"
)

// resolveClientPassword resolves the daemon password for a client process
// (TUI, CLI) that itself does not run the daemon. RUNWISP_PASSWORD is checked
// first; otherwise the value is read from SQLite. The DB is opened, queried,
// and closed before returning so a subsequently-spawned daemon can take the
// write lock cleanly.
func resolveClientPassword() (string, error) {
	if envPw := os.Getenv("RUNWISP_PASSWORD"); envPw != "" {
		return envPw, nil
	}

	cipher, err := secretcipher.FromEnv()
	if err != nil {
		return "", err
	}
	db, err := storage.New(flags.DBPath(), io.Discard, cipher)
	if err != nil {
		return "", err
	}
	defer db.Close()

	password, _, err := datadir.ResolvePassword(db)
	return password, err
}
