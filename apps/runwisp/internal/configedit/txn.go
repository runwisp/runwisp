// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package configedit writes RunWisp's config files. It is the only place that
// puts bytes into an operator's runwisp.toml or into the machine-owned staging
// file, and it exists so the loader in internal/config stays a pure reader —
// load and reload are the daemon's hot path and shouldn't share a package with
// filesystem mutation.
//
// Two ideas carry the package:
//
//   - Txn: a set of config files written as one unit. Every file goes through
//     temp+rename, and a caller-supplied gate (normally "does the merged config
//     still load?") decides whether the write is accepted. If the gate refuses,
//     every touched file is restored to its pre-write bytes. A half-applied
//     multi-file edit would leave the daemon with a config it can't load, or —
//     worse, per Prime Directive #1 — with tasks that load nowhere.
//
//   - Surgical text edits: changes are made to the file's *bytes*, never by
//     re-rendering a parsed document, so the operator's comments, key order and
//     formatting survive untouched.
package configedit

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// DefaultPerm is the mode new config files are created with. Config is not
// secret — it is meant to be read, reviewed, and kept in git — so it gets the
// same 0644 as a hand-authored runwisp.toml. Secrets live under the data dir
// with their own restrictive perms.
const DefaultPerm fs.FileMode = 0o644

// Txn accumulates writes to one or more config files and applies them as a
// single, gated unit. Not safe for concurrent use; a Txn is built and applied by
// one caller in one operation.
type Txn struct {
	queued []queuedWrite
}

type queuedWrite struct {
	path string
	data []byte
	perm fs.FileMode
}

// New returns an empty transaction.
func New() *Txn { return &Txn{} }

// Write queues the full contents of one file. perm applies only when the file is
// created; an existing file keeps its own mode, so rewriting a runwisp.toml the
// operator deliberately locked down to 0600 never loosens it. A queued path may
// be written more than once; the last write wins, and the pre-image captured at
// Apply is still the file's original content. Nothing touches the disk until
// Apply.
func (t *Txn) Write(path string, data []byte, perm fs.FileMode) {
	t.queued = append(t.queued, queuedWrite{path: path, data: data, perm: perm})
}

// Empty reports whether the transaction has nothing to write, so a caller can
// skip Apply (and its gate) entirely.
func (t *Txn) Empty() bool { return len(t.queued) == 0 }

// Apply writes every queued file through temp+rename, then calls gate. When
// gate returns an error — or any write fails — every file the transaction
// touched is restored to exactly its previous state (removed if it didn't exist)
// and that error is returned. A nil gate accepts the write unconditionally.
//
// Parent directories are created as needed and are *not* removed on rollback: an
// empty runwisp.d/ is harmless, and removing a directory we may not have created
// is the riskier of the two behaviors.
func (t *Txn) Apply(gate func() error) error {
	backups := make([]fileBackup, 0, len(t.queued))
	rollback := func() {
		for i := range backups {
			backups[i].restore()
		}
	}

	for _, w := range t.queued {
		backups = append(backups, backupFile(w.path))
		if err := writeFileAtomic(w.path, w.data, w.perm); err != nil {
			rollback()
			return &WriteError{Path: w.path, Err: err}
		}
	}
	if gate == nil {
		return nil
	}
	if err := gate(); err != nil {
		rollback()
		return err
	}
	return nil
}

// WriteError reports a file the transaction could not write. The transaction has
// already been rolled back by the time it is returned.
type WriteError struct {
	Path string
	Err  error
}

func (e *WriteError) Error() string { return fmt.Sprintf("write %s: %v", e.Path, e.Err) }
func (e *WriteError) Unwrap() error { return e.Err }

// fileBackup snapshots a file's prior content so a failed multi-file write can
// be rolled back to exactly its previous state.
type fileBackup struct {
	path    string
	existed bool
	perm    fs.FileMode
	content []byte
}

func backupFile(path string) fileBackup {
	b := fileBackup{path: path, perm: DefaultPerm}
	data, err := os.ReadFile(path)
	if err != nil {
		return b
	}
	b.existed = true
	b.content = data
	if info, err := os.Stat(path); err == nil {
		b.perm = info.Mode().Perm()
	}
	return b
}

// restore returns the file to its snapshotted state: rewriting the prior content
// and mode, or removing the file if it didn't exist before. Best-effort — a
// rollback runs on an already-failing path, and reporting a second error there
// would only bury the first.
func (b fileBackup) restore() {
	if b.existed {
		// WriteFile only applies the mode when it creates the file, and the file
		// exists at this point, so the mode is restored explicitly.
		_ = os.WriteFile(b.path, b.content, b.perm)
		_ = os.Chmod(b.path, b.perm)
		return
	}
	_ = os.Remove(b.path)
}

// writeFileAtomic writes data to path via a temp file in the same directory and
// an atomic rename, so a crash mid-write never leaves a half-written config.
// Missing parent directories are created first. perm is the mode for a new file;
// an existing file keeps the mode it already has.
func writeFileAtomic(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func(err error) error {
		_ = os.Remove(tmpName)
		return err
	}
	_, writeErr := tmp.Write(data)
	closeErr := tmp.Close()
	if writeErr != nil {
		return cleanup(writeErr)
	}
	if closeErr != nil {
		return cleanup(closeErr)
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return cleanup(err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return cleanup(err)
	}
	return nil
}
