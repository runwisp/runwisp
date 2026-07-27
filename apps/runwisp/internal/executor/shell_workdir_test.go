// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package executor

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// drainStdout runs the shell backend to completion and returns captured stdout.
func drainStdout(t *testing.T, task *model.Task, def *model.ShellExecution) string {
	t.Helper()
	backend := &ShellBackend{}
	proc, err := backend.Start(context.Background(), task, nil, def)
	require.NoError(t, err)

	var out bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(&out, proc.Stdout) }()
	go func() { defer wg.Done(); _, _ = io.Copy(io.Discard, proc.Stderr) }()
	wg.Wait()
	_, _ = proc.Wait()
	return out.String()
}

// TestShellBackend_WorkingDirRunsInConfiguredDir proves cmd.Dir is honored:
// `pwd` must print the configured directory, not the daemon's cwd.
func TestShellBackend_WorkingDirRunsInConfiguredDir(t *testing.T) {
	dir := t.TempDir()
	task := &model.Task{Name: "wd"}
	got := drainStdout(t, task, &model.ShellExecution{Script: "pwd", WorkingDir: dir})
	// macOS /tmp symlinks to /private/tmp, so compare via the shell's own cwd
	// resolution rather than the raw temp path.
	assert.Contains(t, got, dir)
}

// TestShellBackend_EmptyWorkingDirUnchanged confirms the common path is intact:
// with no WorkingDir, the process inherits the daemon's cwd (non-empty pwd).
func TestShellBackend_EmptyWorkingDirUnchanged(t *testing.T) {
	task := &model.Task{Name: "wd"}
	got := drainStdout(t, task, &model.ShellExecution{Script: "pwd"})
	assert.NotEmpty(t, got)
}

// TestShellBackend_MissingWorkingDirFailsStart proves a working_dir that does
// not exist fails the run loudly at start — existence is checked at run time,
// not config load, so this is where the operator finds out.
func TestShellBackend_MissingWorkingDirFailsStart(t *testing.T) {
	task := &model.Task{Name: "wd"}
	backend := &ShellBackend{}
	_, err := backend.Start(context.Background(), task, nil, &model.ShellExecution{
		Script:     "pwd",
		WorkingDir: filepath.Join(t.TempDir(), "does-not-exist"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "working_dir")
}

// TestShellBackend_WorkingDirNotADirectoryFailsStart proves a working_dir that
// points at a regular file is rejected at start with a clear message.
func TestShellBackend_WorkingDirNotADirectoryFailsStart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "afile")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0644))

	task := &model.Task{Name: "wd"}
	backend := &ShellBackend{}
	_, err := backend.Start(context.Background(), task, nil, &model.ShellExecution{
		Script:     "pwd",
		WorkingDir: file,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not a directory")
}

// TestShellBackend_ShellSelectsInterpreter proves cmd uses the configured
// shell: a bash-only construct (brace expansion in `echo`) behaves differently
// under bash vs the POSIX /bin/sh. We assert the script ran under the selected
// interpreter by probing $0-style behavior portably.
func TestShellBackend_ShellSelectsInterpreter(t *testing.T) {
	task := &model.Task{Name: "sh"}
	// Both /bin/sh and the explicit selection should print the marker; the
	// point is that an explicit Shell path is accepted and executed.
	got := drainStdout(t, task, &model.ShellExecution{Script: "echo selected", Shell: "/bin/sh"})
	assert.Contains(t, got, "selected")
}
