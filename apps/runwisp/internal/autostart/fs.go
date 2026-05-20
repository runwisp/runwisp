// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package autostart

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// FileSystem is the filesystem seam used by the installer. Production
// uses osFS; tests use FakeFS so they never touch the real disk.
type FileSystem interface {
	Stat(path string) (fs.FileInfo, error)
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte, perm fs.FileMode) error
	Remove(path string) error
	MkdirAll(path string, perm fs.FileMode) error
}

// osFS is the production FileSystem.
type osFS struct{}

func (osFS) Stat(path string) (fs.FileInfo, error)        { return os.Stat(path) }
func (osFS) ReadFile(path string) ([]byte, error)         { return os.ReadFile(path) }
func (osFS) Remove(path string) error                     { return os.Remove(path) }
func (osFS) MkdirAll(path string, perm fs.FileMode) error { return os.MkdirAll(path, perm) }
func (osFS) WriteFile(path string, data []byte, perm fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, data, perm)
}

// NewOSFileSystem returns the production FileSystem.
func NewOSFileSystem() FileSystem { return osFS{} }

// FakeFS is an in-memory FileSystem for tests. Safe for concurrent
// use so table-driven tests can share one instance.
type FakeFS struct {
	mu    sync.Mutex
	files map[string]fakeFile
}

type fakeFile struct {
	data    []byte
	perm    fs.FileMode
	modTime time.Time
	isDir   bool
}

func NewFakeFS() *FakeFS { return &FakeFS{files: map[string]fakeFile{}} }

// fakeFileInfo satisfies fs.FileInfo against an in-memory entry.
type fakeFileInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
	isDir   bool
}

func (i fakeFileInfo) Name() string       { return i.name }
func (i fakeFileInfo) Size() int64        { return i.size }
func (i fakeFileInfo) Mode() fs.FileMode  { return i.mode }
func (i fakeFileInfo) ModTime() time.Time { return i.modTime }
func (i fakeFileInfo) IsDir() bool        { return i.isDir }
func (i fakeFileInfo) Sys() any           { return nil }

func (f *FakeFS) Stat(path string) (fs.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ff, ok := f.files[path]
	if !ok {
		return nil, &fs.PathError{Op: "stat", Path: path, Err: fs.ErrNotExist}
	}
	mode := ff.perm
	if ff.isDir {
		mode |= fs.ModeDir
	}
	return fakeFileInfo{
		name:    filepath.Base(path),
		size:    int64(len(ff.data)),
		mode:    mode,
		modTime: ff.modTime,
		isDir:   ff.isDir,
	}, nil
}

func (f *FakeFS) ReadFile(path string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ff, ok := f.files[path]
	if !ok || ff.isDir {
		return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
	}
	out := make([]byte, len(ff.data))
	copy(out, ff.data)
	return out, nil
}

func (f *FakeFS) WriteFile(path string, data []byte, perm fs.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.mkdirAllLocked(filepath.Dir(path), 0755); err != nil {
		return err
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	f.files[path] = fakeFile{data: cp, perm: perm, modTime: time.Now()}
	return nil
}

func (f *FakeFS) Remove(path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.files[path]; !ok {
		return &fs.PathError{Op: "remove", Path: path, Err: fs.ErrNotExist}
	}
	delete(f.files, path)
	return nil
}

func (f *FakeFS) MkdirAll(path string, perm fs.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mkdirAllLocked(path, perm)
}

func (f *FakeFS) mkdirAllLocked(path string, perm fs.FileMode) error {
	if path == "" || path == "/" || path == "." {
		return nil
	}
	for p := path; p != "/" && p != "." && p != ""; p = filepath.Dir(p) {
		ff, ok := f.files[p]
		if ok && !ff.isDir {
			return errors.New("autostart: path exists and is not a directory: " + p)
		}
		if !ok {
			f.files[p] = fakeFile{isDir: true, perm: perm, modTime: time.Now()}
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}

// Paths returns the sorted set of file paths currently in the FakeFS.
// Useful in test assertions.
func (f *FakeFS) Paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.files))
	for p, ff := range f.files {
		if ff.isDir {
			continue
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
