// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package executor

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestShellBackend_ProcessGroupSIGTERMKillsChildren exercises the new
// setpgid-on-fork + SIGTERM-to-pgid logic: a /bin/sh script that spawns a
// long-lived `sleep` child must die — both shell and child — when the
// task's context is cancelled. The test fails if the child process keeps
// running past the grace window.
func TestShellBackend_ProcessGroupSIGTERMKillsChildren(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")

	task := &model.Task{
		Name:         "pg-test",
		GracefulStop: 200 * time.Millisecond,
	}

	// The script writes the spawned sleep's PID to disk and waits for it.
	// SIGTERM to the leader wouldn't propagate to `sleep`; SIGTERM to the
	// process group does. The /bin/sh leader keeps running because we trap
	// SIGTERM with `wait` semantics, which only proceeds once the child is
	// reaped — exactly the case where naive Process.Kill() would leak.
	script := `sleep 30 &
echo $! > ` + pidFile + `
wait
`

	ctx, cancel := context.WithCancel(context.Background())
	backend := &ShellBackend{}
	proc, err := backend.Start(ctx, task, nil, &model.ShellExecution{Script: script})
	require.NoError(t, err)
	t.Cleanup(func() {
		if proc.Stdout != nil {
			_, _ = io.Copy(io.Discard, proc.Stdout)
		}
		if proc.Stderr != nil {
			_, _ = io.Copy(io.Discard, proc.Stderr)
		}
	})

	go func() {
		if proc.Stdout != nil {
			_, _ = io.Copy(io.Discard, proc.Stdout)
		}
	}()
	go func() {
		if proc.Stderr != nil {
			_, _ = io.Copy(io.Discard, proc.Stderr)
		}
	}()

	// Wait for the script to write the child PID — up to one second.
	var childPid int
	require.Eventually(t, func() bool {
		pid, ok := readPidFromFile(pidFile)
		if !ok {
			return false
		}
		childPid = pid
		return true
	}, time.Second, 20*time.Millisecond, "script must record the child PID")

	cancel()
	exit, _ := proc.Wait()
	// Cancelled processes typically exit non-zero or via signal.
	assert.NotEqual(t, 0, exit, "cancelled shell exits non-zero")

	// Give the kernel a moment to reap.
	require.Eventually(t, func() bool {
		err := syscall.Kill(childPid, 0) // signal 0 = existence probe
		return errors.Is(err, syscall.ESRCH) || os.IsPermission(err)
	}, time.Second, 20*time.Millisecond, "child sleep must be reaped after the leader's process group is signalled")
}

// TestShellBackend_ImmediateKillWhenGracefulStopZero verifies that
// graceful_stop = 0 sends SIGKILL to the process group with no SIGTERM
// detour. The script traps SIGTERM and only exits on SIGKILL — so a
// successful exit proves the kill path skipped SIGTERM.
func TestShellBackend_ImmediateKillWhenGracefulStopZero(t *testing.T) {
	task := &model.Task{
		Name:         "insta-kill",
		GracefulStop: 0,
	}
	// trap SIGTERM '' makes the shell ignore SIGTERM forever; the only way
	// out is SIGKILL.
	script := `trap '' TERM
sleep 30
`

	ctx, cancel := context.WithCancel(context.Background())
	backend := &ShellBackend{}
	proc, err := backend.Start(ctx, task, nil, &model.ShellExecution{Script: script})
	require.NoError(t, err)
	go func() { _, _ = io.Copy(io.Discard, proc.Stdout) }()
	go func() { _, _ = io.Copy(io.Discard, proc.Stderr) }()

	// Give the trap time to install.
	time.Sleep(50 * time.Millisecond)
	cancel()

	// The kill goroutine fires SIGKILL on the group immediately, so Wait
	// must return well within one second even though the script is sleeping
	// for 30s with SIGTERM trapped.
	done := make(chan struct{})
	go func() { proc.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("graceful_stop=0 must SIGKILL immediately, not wait")
	}
}

// readPidFromFile loads a PID written by the shell-script helper. Returns
// (pid, true) only when the file is non-empty and parses as a positive int.
func readPidFromFile(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return 0, false
	}
	var pid int
	if _, scanErr := fmtSscanInt(string(data), &pid); scanErr != nil {
		return 0, false
	}
	if pid <= 0 {
		return 0, false
	}
	return pid, true
}

// fmtSscanInt is a tiny Sscanf replacement to keep this test free of
// fmt-package import in the helper signature; the real fmt package is fine
// to use directly elsewhere but a focused parser keeps the intent clear.
func fmtSscanInt(s string, out *int) (int, error) {
	v := 0
	count := 0
	for _, ch := range s {
		if ch == '\n' || ch == '\r' || ch == ' ' || ch == '\t' {
			if count > 0 {
				break
			}
			continue
		}
		if ch < '0' || ch > '9' {
			return count, errors.New("non-digit")
		}
		v = v*10 + int(ch-'0')
		count++
	}
	*out = v
	return count, nil
}
