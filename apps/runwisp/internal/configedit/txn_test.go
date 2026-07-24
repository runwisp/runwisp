// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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
