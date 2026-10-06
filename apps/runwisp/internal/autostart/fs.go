// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package autostart

import (
	"io/fs"
	"os"
	"path/filepath"
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
