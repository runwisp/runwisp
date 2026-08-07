// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
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
	// remove inverts the operation: delete the file instead of writing data.
	remove bool
	// preserveOwner routes the write through writeFileAtomicPreservingOwner
	// instead of writeFileAtomic: the file must already exist, and its uid/gid
	// are carried onto the replacement, not just its mode. See WritePreservingOwner.
	preserveOwner bool
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

// Remove queues the deletion of a file. Its pre-image is captured at Apply like
// any other operation, so a refused gate brings the file back byte-for-byte,
// mode included. Deleting a path that doesn't exist is not an error — the
// transaction's job is to reach the requested end state.
//
// This is how `promote` retires the staging file once its last entry has moved
// into the operator's own config: an empty runwisp.d/imported.toml would only
// look like it still had something to say.
func (t *Txn) Remove(path string) {
	t.queued = append(t.queued, queuedWrite{path: path, remove: true})
}

// WritePreservingOwner queues a full rewrite of a file that must land under the
// uid/gid it already has, not just its mode — the crontab case, where the
// daemon may run as root but a per-user spool file has to stay owned by the
// account whose jobs it holds. Unlike Write, the file must already exist: a
// rewrite that can't confirm the prior owner refuses (see
// writeFileAtomicPreservingOwner) rather than silently landing owned by
// whoever the daemon runs as.
func (t *Txn) WritePreservingOwner(path string, data []byte) {
	t.queued = append(t.queued, queuedWrite{path: path, data: data, preserveOwner: true})
}

// Empty reports whether the transaction has nothing to write, so a caller can
// skip Apply (and its gate) entirely.
func (t *Txn) Empty() bool { return len(t.queued) == 0 }

// Apply writes every queued file through temp+rename (or removes it, for a
// queued Remove), then calls gate. When gate returns an error — or any operation
// fails — every file the transaction touched is restored to exactly its previous
// state (removed if it didn't exist, recreated if it did) and that error is
// returned. A nil gate accepts the write unconditionally.
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
		if err := w.perform(); err != nil {
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

// perform carries out one queued operation against the filesystem.
func (w queuedWrite) perform() error {
	if w.remove {
		if err := os.Remove(w.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if w.preserveOwner {
		return writeFileAtomicPreservingOwner(w.path, w.data)
	}
	return writeFileAtomic(w.path, w.data, w.perm)
}

// WriteError reports a file the transaction could not write or remove. The
// transaction has already been rolled back by the time it is returned.
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
	return atomicReplace(path, data, perm, nil)
}

// writeFileAtomicPreservingOwner is writeFileAtomic's crontab variant. Plain
// writeFileAtomic's temp+rename leaves the replacement owned by whoever the
// daemon runs as — fine for a runwisp.toml the daemon itself owns, wrong for a
// per-user spool crontab a root daemon is editing on another account's behalf.
// The file must already exist (a crontab this rewrites was always read off
// disk first); if its owner can't be determined, this refuses rather than
// landing the file mis-owned.
func writeFileAtomicPreservingOwner(path string, data []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot determine %s's owner on this platform; refusing to rewrite it", path)
	}
	owner := &ownerID{uid: int(stat.Uid), gid: int(stat.Gid)}
	return atomicReplace(path, data, info.Mode().Perm(), owner)
}

// ownerID is the uid/gid a rewritten file must be chowned back to.
type ownerID struct{ uid, gid int }

// atomicReplace is the shared temp-file-then-rename mechanics behind both
// write paths above. owner, when non-nil, chowns the temp file before the
// rename; a failed chown is treated the same as a failed write — cleaned up
// and reported, never silently skipped.
func atomicReplace(path string, data []byte, perm fs.FileMode, owner *ownerID) error {
	dir := filepath.Dir(path)
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
	if owner != nil {
		if err := os.Chown(tmpName, owner.uid, owner.gid); err != nil {
			return cleanup(fmt.Errorf("preserve owner of %s: %w", path, err))
		}
	}
	if err := os.Rename(tmpName, path); err != nil {
		return cleanup(err)
	}
	return nil
}
