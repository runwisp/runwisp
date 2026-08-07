// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package configedit

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTxn_AppliesEveryQueuedFile(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.toml")
	b := filepath.Join(dir, "sub", "b.toml")

	txn := New()
	txn.Write(a, []byte("a\n"), DefaultPerm)
	txn.Write(b, []byte("b\n"), DefaultPerm)
	require.NoError(t, txn.Apply(nil))

	assert.Equal(t, "a\n", readFile(t, a))
	assert.Equal(t, "b\n", readFile(t, b), "a missing parent dir must be created")
}

// TestTxn_GateFailureRestoresEveryPreImage is the guarantee the whole package
// exists for: a rejected multi-file write must leave the operator's config
// exactly as it was, not partially updated. An existing file goes back to its
// original bytes; a file that didn't exist goes back to not existing.
func TestTxn_GateFailureRestoresEveryPreImage(t *testing.T) {
	dir := writeFileTree(t, map[string]string{"existing.toml": "original\n"})
	existing := filepath.Join(dir, "existing.toml")
	fresh := filepath.Join(dir, "fresh.toml")

	txn := New()
	txn.Write(existing, []byte("rewritten\n"), DefaultPerm)
	txn.Write(fresh, []byte("new\n"), DefaultPerm)

	sentinel := errors.New("gate says no")
	err := txn.Apply(func() error { return sentinel })
	require.ErrorIs(t, err, sentinel)

	assert.Equal(t, "original\n", readFile(t, existing), "must be byte-identical to the pre-image")
	_, statErr := os.Stat(fresh)
	assert.True(t, os.IsNotExist(statErr), "a file that didn't exist must not survive a rollback")
}

// TestTxn_RemoveDeletesTheFile covers `promote` retiring the staging file once
// its last entry has moved into the operator's own config.
func TestTxn_RemoveDeletesTheFile(t *testing.T) {
	dir := writeFileTree(t, map[string]string{"staging.toml": "[tasks.a]\n"})
	staging := filepath.Join(dir, "staging.toml")

	txn := New()
	txn.Remove(staging)
	require.NoError(t, txn.Apply(nil))

	_, err := os.Stat(staging)
	assert.True(t, os.IsNotExist(err))
}

// TestTxn_RemoveOfAMissingFileIsNotAnError: the transaction's contract is the end
// state, not the sequence of steps that got there.
func TestTxn_RemoveOfAMissingFileIsNotAnError(t *testing.T) {
	txn := New()
	txn.Remove(filepath.Join(t.TempDir(), "never-existed.toml"))
	assert.NoError(t, txn.Apply(nil))
}

// TestTxn_GateFailureRestoresARemovedFile is the rollback half of Remove: a
// promote whose merged load fails must bring the staging file back, or the tasks
// it held would vanish from the config entirely (Prime Directive #1).
func TestTxn_GateFailureRestoresARemovedFile(t *testing.T) {
	dir := writeFileTree(t, map[string]string{"staging.toml": "[tasks.a]\nrun = \"a\"\n"})
	staging := filepath.Join(dir, "staging.toml")
	require.NoError(t, os.Chmod(staging, 0o640))

	txn := New()
	txn.Remove(staging)

	sentinel := errors.New("gate says no")
	require.ErrorIs(t, txn.Apply(func() error { return sentinel }), sentinel)

	assert.Equal(t, "[tasks.a]\nrun = \"a\"\n", readFile(t, staging))
	info, err := os.Stat(staging)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), info.Mode().Perm(), "the restored file keeps its mode")
}

// TestTxn_GateSeesTheWrittenFiles pins the ordering the merged-load gate depends
// on: every queued file is on disk before the gate runs.
func TestTxn_GateSeesTheWrittenFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.toml")

	var seen string
	txn := New()
	txn.Write(path, []byte("staged\n"), DefaultPerm)
	require.NoError(t, txn.Apply(func() error {
		seen = readFile(t, path)
		return nil
	}))
	assert.Equal(t, "staged\n", seen)
}

// TestTxn_PreservesExistingMode covers a config the operator deliberately locked
// down (a runwisp.toml with inline secrets, say). Rewriting it — or rolling that
// rewrite back — must not quietly widen its permissions to the default.
func TestTxn_PreservesExistingMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		gate func() error
	}{
		{name: "successful write", gate: nil},
		{name: "rolled-back write", gate: func() error { return errors.New("no") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "a.toml")
			require.NoError(t, os.WriteFile(path, []byte("original\n"), 0o600))

			txn := New()
			txn.Write(path, []byte("rewritten\n"), DefaultPerm)
			_ = txn.Apply(tc.gate)

			info, err := os.Stat(path)
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		})
	}
}

func TestTxn_NewFileGetsTheRequestedMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.toml")

	txn := New()
	txn.Write(path, []byte("a\n"), DefaultPerm)
	require.NoError(t, txn.Apply(nil))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(DefaultPerm), info.Mode().Perm())
}

func TestTxn_WriteFailureRollsBackEarlierFiles(t *testing.T) {
	dir := writeFileTree(t, map[string]string{"first.toml": "original\n"})
	first := filepath.Join(dir, "first.toml")
	// A path whose parent is an existing *file* can't be created as a directory,
	// so the second write fails without needing permission games.
	unwritable := filepath.Join(first, "nested", "second.toml")

	txn := New()
	txn.Write(first, []byte("rewritten\n"), DefaultPerm)
	txn.Write(unwritable, []byte("second\n"), DefaultPerm)

	err := txn.Apply(nil)
	var we *WriteError
	require.ErrorAs(t, err, &we)
	assert.Equal(t, unwritable, we.Path)
	assert.Equal(t, "original\n", readFile(t, first))
}

func TestTxn_EmptyReportsNothingQueued(t *testing.T) {
	txn := New()
	assert.True(t, txn.Empty())
	txn.Write("/tmp/x", nil, DefaultPerm)
	assert.False(t, txn.Empty())
}

// TestTxn_LeavesNoTempFiles guards against the temp+rename mechanism littering
// the operator's config dir when a write is rolled back.
func TestTxn_LeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	txn := New()
	txn.Write(filepath.Join(dir, "a.toml"), []byte("a\n"), DefaultPerm)
	require.Error(t, txn.Apply(func() error { return errors.New("no") }))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "rollback must remove the file it created and leave no temp files")
}

// TestTxn_WritePreservingOwnerKeepsMode is the crontab case: promote rewrites a
// file it doesn't own the format of, and the rewrite must not touch its mode
// even though — unlike a plain Write — the caller supplies no perm at all.
func TestTxn_WritePreservingOwnerKeepsMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crontab")
	require.NoError(t, os.WriteFile(path, []byte("original\n"), 0o640))

	txn := New()
	txn.WritePreservingOwner(path, []byte("rewritten\n"))
	require.NoError(t, txn.Apply(nil))

	assert.Equal(t, "rewritten\n", readFile(t, path))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), info.Mode().Perm())
}

// TestTxn_WritePreservingOwnerRefusesAMissingFile: unlike Write, there is no
// sensible "create it" behaviour here — a crontab this queues a rewrite for
// was always read off disk first, so its absence means it moved out from
// under the promote and the rewrite must refuse rather than invent a new file
// with no owner to preserve.
func TestTxn_WritePreservingOwnerRefusesAMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "gone")

	txn := New()
	txn.WritePreservingOwner(path, []byte("x\n"))
	err := txn.Apply(nil)
	require.Error(t, err)

	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr), "must not create the file it couldn't confirm an owner for")
}

// TestTxn_WritePreservingOwnerRollsBackOnFailure covers a WritePreservingOwner
// queued alongside a write that later fails: the crontab rewrite must unwind
// exactly like an ordinary Write does.
func TestTxn_WritePreservingOwnerRollsBackOnFailure(t *testing.T) {
	dir := writeFileTree(t, map[string]string{"crontab": "original\n"})
	crontab := filepath.Join(dir, "crontab")

	txn := New()
	txn.WritePreservingOwner(crontab, []byte("rewritten\n"))

	sentinel := errors.New("gate says no")
	require.ErrorIs(t, txn.Apply(func() error { return sentinel }), sentinel)
	assert.Equal(t, "original\n", readFile(t, crontab))
}
