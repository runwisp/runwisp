// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package autostart

import (
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFakeFS_FileInfoFields(t *testing.T) {
	f := NewFakeFS()
	require.NoError(t, f.WriteFile("/a/b/file.txt", []byte("hello"), 0o644))

	info, err := f.Stat("/a/b/file.txt")
	require.NoError(t, err)
	assert.Equal(t, "file.txt", info.Name())
	assert.Equal(t, int64(5), info.Size())
	assert.Equal(t, fs.FileMode(0o644), info.Mode())
	assert.False(t, info.IsDir())
	assert.False(t, info.ModTime().IsZero())
	assert.Nil(t, info.Sys())
}

func TestFakeFS_StatOnDirSetsModeDir(t *testing.T) {
	f := NewFakeFS()
	require.NoError(t, f.MkdirAll("/etc/systemd", 0o755))

	info, err := f.Stat("/etc/systemd")
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	assert.NotZero(t, info.Mode()&fs.ModeDir)
}

func TestFakeFS_StatMissing(t *testing.T) {
	f := NewFakeFS()
	_, err := f.Stat("/missing")
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

func TestFakeFS_ReadFileOnDirErrors(t *testing.T) {
	f := NewFakeFS()
	require.NoError(t, f.MkdirAll("/dir", 0o755))
	_, err := f.ReadFile("/dir")
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

func TestFakeFS_ReadFileMissing(t *testing.T) {
	f := NewFakeFS()
	_, err := f.ReadFile("/no/such")
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

func TestFakeFS_RemoveMissingErrors(t *testing.T) {
	f := NewFakeFS()
	err := f.Remove("/no/such")
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

func TestFakeFS_RemoveExisting(t *testing.T) {
	f := NewFakeFS()
	require.NoError(t, f.WriteFile("/a/file.txt", []byte("x"), 0o600))
	require.NoError(t, f.Remove("/a/file.txt"))

	_, err := f.Stat("/a/file.txt")
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

func TestFakeFS_WriteFileCopiesData(t *testing.T) {
	f := NewFakeFS()
	data := []byte("original")
	require.NoError(t, f.WriteFile("/a.txt", data, 0o600))

	data[0] = 'X'

	got, err := f.ReadFile("/a.txt")
	require.NoError(t, err)
	assert.Equal(t, "original", string(got), "stored data unaffected by caller mutation")

	// Returned slice is also a copy.
	got[0] = 'Y'
	again, err := f.ReadFile("/a.txt")
	require.NoError(t, err)
	assert.Equal(t, "original", string(again))
}

func TestFakeFS_MkdirAllNested(t *testing.T) {
	f := NewFakeFS()
	require.NoError(t, f.MkdirAll("/a/b/c", 0o755))

	for _, p := range []string{"/a", "/a/b", "/a/b/c"} {
		info, err := f.Stat(p)
		require.NoError(t, err, "stat %q", p)
		assert.True(t, info.IsDir(), "%q is a dir", p)
	}
}

func TestFakeFS_MkdirAllNoopsAtRoot(t *testing.T) {
	f := NewFakeFS()
	assert.NoError(t, f.MkdirAll("", 0o755))
	assert.NoError(t, f.MkdirAll("/", 0o755))
	assert.NoError(t, f.MkdirAll(".", 0o755))
}

func TestFakeFS_MkdirAllConflictWithFile(t *testing.T) {
	f := NewFakeFS()
	require.NoError(t, f.WriteFile("/etc", []byte("not a dir"), 0o600))

	err := f.MkdirAll("/etc/systemd", 0o755)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a directory")
}

func TestFakeFS_Paths(t *testing.T) {
	f := NewFakeFS()
	require.NoError(t, f.MkdirAll("/dir", 0o755))
	require.NoError(t, f.WriteFile("/dir/b.txt", []byte("b"), 0o600))
	require.NoError(t, f.WriteFile("/dir/a.txt", []byte("a"), 0o600))

	got := f.Paths()
	assert.Equal(t, []string{"/dir/a.txt", "/dir/b.txt"}, got, "files only, sorted")
}

func TestOSFS_WriteFile_MkdirError(t *testing.T) {
	// Writing under a path whose parent already exists as a file should fail at
	// the MkdirAll step inside osFS.WriteFile.
	fs := NewOSFileSystem()
	dir := t.TempDir()
	parentAsFile := dir + "/blocker"
	require.NoError(t, fs.WriteFile(parentAsFile, []byte("x"), 0o600))

	err := fs.WriteFile(parentAsFile+"/child.txt", []byte("y"), 0o600)
	require.Error(t, err, "MkdirAll should fail when parent is a regular file")
}
