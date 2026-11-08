// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"bufio"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/bootid"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startGroup starts script under sh leading its own group, like startCmd
// does, waits for it to print its first line (so traps are set), and returns
// a channel closed once it has exited and been reaped.
func startGroup(t *testing.T, script string) (*exec.Cmd, <-chan struct{}) {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, cmd.Start())
	_, err = bufio.NewReader(stdout).ReadString('\n')
	require.NoError(t, err)
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); <-done })
	return cmd, done
}

func stopTask(grace time.Duration) *model.Task {
	return &model.Task{StopSignal: "SIGTERM", GracefulStop: &grace}
}

func waitExited(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("orphaned group should be stopped")
	}
}

// A job that handles the stop signal gets to exit on its own, well before the
// grace window ends.
func TestStopOrphanGroupSendsStopSignalFirst(t *testing.T) {
	cmd, done := startGroup(t, "trap 'exit 7' TERM; echo ready; while :; do sleep 0.05; done")
	start := bootid.Process(cmd.Process.Pid)
	require.NotEmpty(t, start)

	began := time.Now()
	assert.True(t, StopOrphanGroup(cmd.Process.Pid, start, stopTask(time.Minute), time.Minute))
	assert.Less(t, time.Since(began), 5*time.Second, "must not sit out the grace window once the group is gone")
	waitExited(t, done)
	assert.Equal(t, 7, cmd.ProcessState.ExitCode(), "the job's own stop handler should run")
}

// A job that ignores the stop signal is SIGKILLed once the capped grace runs out.
func TestStopOrphanGroupKillsAfterGrace(t *testing.T) {
	cmd, done := startGroup(t, "trap '' TERM; echo ready; while :; do sleep 0.05; done")
	start := bootid.Process(cmd.Process.Pid)
	require.NotEmpty(t, start)

	assert.True(t, StopOrphanGroup(cmd.Process.Pid, start, stopTask(time.Minute), 200*time.Millisecond))
	waitExited(t, done)
	ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	require.True(t, ok)
	assert.Equal(t, syscall.SIGKILL, ws.Signal())
}

// A stale row must never signal whatever holds the pid now.
func TestStopOrphanGroupSparesMismatch(t *testing.T) {
	cmd, done := startGroup(t, "echo ready; sleep 30")
	start := bootid.Process(cmd.Process.Pid)

	assert.False(t, StopOrphanGroup(cmd.Process.Pid, "some-other-boot#1", nil, 0))
	assert.False(t, StopOrphanGroup(cmd.Process.Pid, "", nil, 0))
	assert.False(t, StopOrphanGroup(0, start, nil, 0))
	select {
	case <-done:
		t.Fatal("a process whose identity doesn't match must be left alone")
	case <-time.After(100 * time.Millisecond):
	}
}
