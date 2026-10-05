// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package configedit

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
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

// TestTxn_GateFailureRestoresOriginalWhenPathWrittenTwice is the same
// guarantee as TestTxn_GateFailureRestoresEveryPreImage, but for a path
// queued more than once in the same Txn (Write's own doc: "the pre-image
// captured at Apply is still the file's original content"). A rollback must
// land back on the true original, not on the intermediate write.
func TestTxn_GateFailureRestoresOriginalWhenPathWrittenTwice(t *testing.T) {
	dir := writeFileTree(t, map[string]string{"existing.toml": "original\n"})
	existing := filepath.Join(dir, "existing.toml")

	txn := New()
	txn.Write(existing, []byte("intermediate\n"), DefaultPerm)
	txn.Write(existing, []byte("final\n"), DefaultPerm)

	sentinel := errors.New("gate says no")
	err := txn.Apply(func() error { return sentinel })
	require.ErrorIs(t, err, sentinel)

	assert.Equal(t, "original\n", readFile(t, existing), "must roll back to the true original, not the intermediate write")
}

// TestTxn_RepeatedWriteCollapsesAtQueueTime pins down where "last write wins"
// is enforced: at Write time, not at Apply. A repeat write to an already-queued
// path replaces the earlier entry in place rather than piling up a second one,
// so Apply never performs (and then discards) an intermediate write.
func TestTxn_RepeatedWriteCollapsesAtQueueTime(t *testing.T) {
	txn := New()
	txn.Write("/a", []byte("first"), DefaultPerm)
	txn.Write("/b", []byte("other"), DefaultPerm)
	txn.Write("/a", []byte("second"), DefaultPerm)

	require.Len(t, txn.queued, 2)
	for _, w := range txn.queued {
		if w.path == "/a" {
			assert.Equal(t, []byte("second"), w.data)
		}
	}
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
// it held would vanish from the config entirely.
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

// TestFileBackup_RestoreUsesAtomicReplace guards the crash-safety fix for
// restore(): it used to rewrite an existing file in place via os.WriteFile,
// which is not crash-safe — a kill mid-write can leave a torn file, exactly
// what this package's "every file goes through temp+rename" contract exists to
// prevent. restore must now replace the file the same way every forward write
// does. Exercised directly against backupFile/restore (bypassing Txn.Apply,
// whose own forward write already rotates the inode once) so the assertion
// isolates restore's own behavior: an in-place rewrite leaves the inode from
// the intervening write untouched, a rename-based replace does not.
func TestFileBackup_RestoreUsesAtomicReplace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.toml")
	require.NoError(t, os.WriteFile(path, []byte("original\n"), 0o644))
	b := backupFile(path)

	// The write this backup exists to undo — an in-place rewrite, so it does
	// not itself touch the inode.
	require.NoError(t, os.WriteFile(path, []byte("changed\n"), 0o644))
	changed, err := os.Stat(path)
	require.NoError(t, err)
	changedIno := changed.Sys().(*syscall.Stat_t).Ino

	b.restore(hostDisk())

	assert.Equal(t, "original\n", readFile(t, path))
	after, err := os.Stat(path)
	require.NoError(t, err)
	afterIno := after.Sys().(*syscall.Stat_t).Ino
	assert.NotEqual(t, changedIno, afterIno,
		"restore must replace the file via rename (a new inode), not rewrite it in place")
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
// exactly like an ordinary Write does. It must also land back under the file's
// original owner: restore replaces the file via rename, which — unlike an
// in-place rewrite — takes on the temp file's own ownership unless the backup
// explicitly chowns it back.
func TestTxn_WritePreservingOwnerRollsBackOnFailure(t *testing.T) {
	dir := writeFileTree(t, map[string]string{"crontab": "original\n"})
	crontab := filepath.Join(dir, "crontab")
	before, err := os.Stat(crontab)
	require.NoError(t, err)
	beforeOwner := before.Sys().(*syscall.Stat_t)

	txn := New()
	txn.WritePreservingOwner(crontab, []byte("rewritten\n"))

	sentinel := errors.New("gate says no")
	require.ErrorIs(t, txn.Apply(func() error { return sentinel }), sentinel)
	assert.Equal(t, "original\n", readFile(t, crontab))

	after, err := os.Stat(crontab)
	require.NoError(t, err)
	afterOwner := after.Sys().(*syscall.Stat_t)
	assert.Equal(t, beforeOwner.Uid, afterOwner.Uid, "restore must preserve the original owner")
	assert.Equal(t, beforeOwner.Gid, afterOwner.Gid, "restore must preserve the original group")
}

// TestTxn_WriteThroughSymlinkKeepsTheLink: a runwisp.toml symlinked into a
// dotfiles repo used to be replaced by a plain file (rename swaps the link
// itself), silently forking the config from the repo. The edit must land in
// the link's target and the link must survive, including across a rollback.
func TestTxn_WriteThroughSymlinkKeepsTheLink(t *testing.T) {
	repo := writeFileTree(t, map[string]string{"runwisp.toml": "original\n"})
	target := filepath.Join(repo, "runwisp.toml")
	link := filepath.Join(t.TempDir(), "runwisp.toml")
	require.NoError(t, os.Symlink(target, link))

	txn := New()
	txn.Write(link, []byte("changed\n"), DefaultPerm)
	require.NoError(t, txn.Apply(nil))

	assertSymlink(t, link, target)
	assert.Equal(t, "changed\n", readFile(t, target))

	txn = New()
	txn.Write(link, []byte("refused\n"), DefaultPerm)
	require.Error(t, txn.Apply(func() error { return errors.New("no") }))

	assertSymlink(t, link, target)
	assert.Equal(t, "changed\n", readFile(t, target))
}

// TestTxn_WriteThroughRelativeSymlinkChain covers a relative link pointing at
// another link: each hop resolves against the directory of the link holding it.
func TestTxn_WriteThroughRelativeSymlinkChain(t *testing.T) {
	dir := writeFileTree(t, map[string]string{"real/runwisp.toml": "original\n"})
	require.NoError(t, os.Symlink("real/runwisp.toml", filepath.Join(dir, "hop.toml")))
	require.NoError(t, os.Symlink("hop.toml", filepath.Join(dir, "runwisp.toml")))

	txn := New()
	txn.Write(filepath.Join(dir, "runwisp.toml"), []byte("changed\n"), DefaultPerm)
	require.NoError(t, txn.Apply(nil))

	assertSymlink(t, filepath.Join(dir, "runwisp.toml"), "hop.toml")
	assertSymlink(t, filepath.Join(dir, "hop.toml"), "real/runwisp.toml")
	assert.Equal(t, "changed\n", readFile(t, filepath.Join(dir, "real", "runwisp.toml")))
}

func TestTxn_SymlinkLoopIsAWriteError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.Symlink("runwisp.toml", path))

	txn := New()
	txn.Write(path, []byte("x\n"), DefaultPerm)
	var we *WriteError
	require.ErrorAs(t, txn.Apply(nil), &we)
	assert.Equal(t, path, we.Path)
}

func assertSymlink(t *testing.T, link, wantTarget string) {
	t.Helper()
	got, err := os.Readlink(link)
	require.NoError(t, err, "%s must still be a symlink", link)
	assert.Equal(t, wantTarget, got)
}

// recordingDisk is a diskOps whose chown and fsync record their calls instead
// of (or before) touching the disk, standing in for root and for a power cut.
type recordingDisk struct {
	chowns []string
	fsyncs []string
	// onFsync, when set, runs before each recorded fsync.
	onFsync func(name string)
}

func (r *recordingDisk) ops(euid int) diskOps {
	return diskOps{
		euid: euid,
		chown: func(name string, uid, gid int) error {
			r.chowns = append(r.chowns, fmt.Sprintf("%s %d:%d", filepath.Base(name), uid, gid))
			return nil
		},
		fsync: func(f *os.File) error {
			if r.onFsync != nil {
				r.onFsync(f.Name())
			}
			r.fsyncs = append(r.fsyncs, f.Name())
			return f.Sync()
		},
	}
}

// TestTxn_RootWriteKeepsTheOriginalOwner: a root edit of a user-owned
// runwisp.toml used to hand the file to root, because rename installs the temp
// file with the writer's own uid/gid. As root the temp file must be chowned to
// the original owner before the rename.
func TestTxn_RootWriteKeepsTheOriginalOwner(t *testing.T) {
	dir := writeFileTree(t, map[string]string{"runwisp.toml": "original\n"})
	path := filepath.Join(dir, "runwisp.toml")
	info, err := os.Stat(path)
	require.NoError(t, err)
	st := info.Sys().(*syscall.Stat_t)

	var rec recordingDisk
	txn := New()
	txn.disk = rec.ops(0)
	txn.Write(path, []byte("changed\n"), DefaultPerm)
	require.NoError(t, txn.Apply(nil))

	require.Len(t, rec.chowns, 1)
	assert.Regexp(t, fmt.Sprintf(`^\.runwisp\.toml\.tmp-\d+ %d:%d$`, st.Uid, st.Gid), rec.chowns[0],
		"the temp file, not the target, is chowned to the original owner")
}

// TestTxn_NonRootWriteDoesNotChown: an unprivileged writer can't give files
// away, and the temp file already carries its own uid, so no chown is tried.
func TestTxn_NonRootWriteDoesNotChown(t *testing.T) {
	dir := writeFileTree(t, map[string]string{"runwisp.toml": "original\n"})

	var rec recordingDisk
	txn := New()
	txn.disk = rec.ops(1000)
	txn.Write(filepath.Join(dir, "runwisp.toml"), []byte("changed\n"), DefaultPerm)
	require.NoError(t, txn.Apply(nil))
	assert.Empty(t, rec.chowns)
}

// TestTxn_RootWriteKeepsTheOriginalOwnerOnDisk is the same guarantee with a
// real chown; it needs root to own a file as somebody else.
func TestTxn_RootWriteKeepsTheOriginalOwnerOnDisk(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to create a file owned by another user")
	}
	dir := writeFileTree(t, map[string]string{"runwisp.toml": "original\n"})
	path := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.Chown(path, 4242, 4343))

	txn := New()
	txn.Write(path, []byte("changed\n"), DefaultPerm)
	require.NoError(t, txn.Apply(nil))

	info, err := os.Stat(path)
	require.NoError(t, err)
	st := info.Sys().(*syscall.Stat_t)
	assert.Equal(t, uint32(4242), st.Uid)
	assert.Equal(t, uint32(4343), st.Gid)
}

// TestTxn_FsyncsTheFileBeforeRenameAndTheDirAfter: without an fsync of the
// temp file before the rename, a power loss can leave the new name pointing at
// an empty or truncated file; without one of the directory after, the rename
// itself can be lost. Both apply to plain writes, crontab rewrites, and removes.
func TestTxn_FsyncsTheFileBeforeRenameAndTheDirAfter(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml": "original\n",
		"crontab":      "original\n",
		"gone.toml":    "x\n",
	})
	path := filepath.Join(dir, "runwisp.toml")
	crontab := filepath.Join(dir, "crontab")

	var rec recordingDisk
	var seen []string
	rec.onFsync = func(name string) {
		seen = append(seen, readFile(t, path)+"|"+readFile(t, crontab))
	}
	txn := New()
	txn.disk = rec.ops(os.Geteuid())
	txn.Write(path, []byte("changed\n"), DefaultPerm)
	txn.WritePreservingOwner(crontab, []byte("changed\n"))
	txn.Remove(filepath.Join(dir, "gone.toml"))
	require.NoError(t, txn.Apply(nil))

	require.Len(t, rec.fsyncs, 5)
	assert.Regexp(t, `/\.runwisp\.toml\.tmp-\d+$`, rec.fsyncs[0])
	assert.Equal(t, "original\n|original\n", seen[0], "temp file synced before the rename")
	assert.Equal(t, dir, rec.fsyncs[1])
	assert.Equal(t, "changed\n|original\n", seen[1], "dir synced after the rename")
	assert.Regexp(t, `/\.crontab\.tmp-\d+$`, rec.fsyncs[2])
	assert.Equal(t, "changed\n|original\n", seen[2])
	assert.Equal(t, dir, rec.fsyncs[3])
	assert.Equal(t, "changed\n|changed\n", seen[3])
	assert.Equal(t, dir, rec.fsyncs[4], "dir synced after the remove")
}
