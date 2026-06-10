// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExitCodeFromRun_Nil(t *testing.T) {
	assert.Equal(t, 0, exitCodeFromRun(nil))
}

func TestExitCodeFromRun_Success(t *testing.T) {
	r := model.ReasonSuccess
	run := &model.Run{ExitCode: 0, EndReason: &r}
	assert.Equal(t, 0, exitCodeFromRun(run))
}

func TestExitCodeFromRun_FailedPropagatesExitCode(t *testing.T) {
	r := model.ReasonFailed
	run := &model.Run{ExitCode: 42, EndReason: &r}
	assert.Equal(t, 42, exitCodeFromRun(run))
}

func TestExitCodeFromRun_NoEndReason(t *testing.T) {
	run := &model.Run{ExitCode: 99, EndReason: nil}
	assert.Equal(t, 0, exitCodeFromRun(run))
}

func TestIsDaemonRunning_NoPidFile(t *testing.T) {
	t.Parallel()
	assert.False(t, isDaemonRunning(Flags{DataDir: t.TempDir()}), "no PID file → not running")
}

func TestIsDaemonRunning_StalePidFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// PID 0 cannot be signaled — write it so processAlive returns false.
	pidPath := filepath.Join(dir, "daemon.pid")
	require.NoError(t, os.WriteFile(pidPath, []byte(strconv.Itoa(0)), 0o600))

	assert.False(t, isDaemonRunning(Flags{DataDir: dir}), "PID file present but PID dead → not running")
}

func TestIsDaemonRunning_LivePid(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "daemon.pid")
	require.NoError(t, os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o600))

	assert.True(t, isDaemonRunning(Flags{DataDir: dir}), "PID file present and live PID → running")
}

func TestExecLogLineHandler_FiltersByTaskName(t *testing.T) {
	// Wrong-task events route nowhere; verify the handler is a no-op
	// (it does not panic and produces no observable output for the wrong
	// task). Cross-type assertion failure is unreachable because EventData
	// is a sealed interface.
	h := execLogLineHandler("target")
	h(events.Event{Data: events.LogLineEvent{TaskName: "other", Text: "x"}})
	// RunEvent reaches the wrong-type branch (LogLineEvent type assertion fails).
	h(events.Event{Data: events.RunEvent{}})
}

func TestExecLogLineHandler_WritesToStdoutAndStderr(t *testing.T) {
	// Redirect stdout/stderr to pipes so we can verify routing.
	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, err := os.Pipe()
	require.NoError(t, err)
	rErr, wErr, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = wOut
	os.Stderr = wErr
	t.Cleanup(func() {
		os.Stdout = oldOut
		os.Stderr = oldErr
	})

	h := execLogLineHandler("target")
	h(events.Event{Data: events.LogLineEvent{TaskName: "target", Stream: logutil.StreamStdout, Text: "hello stdout"}})
	h(events.Event{Data: events.LogLineEvent{TaskName: "target", Stream: logutil.StreamStderr, Text: "hello stderr"}})

	require.NoError(t, wOut.Close())
	require.NoError(t, wErr.Close())

	outBytes := make([]byte, 64)
	n, _ := rOut.Read(outBytes)
	assert.Contains(t, string(outBytes[:n]), "hello stdout")

	errBytes := make([]byte, 64)
	n, _ = rErr.Read(errBytes)
	assert.Contains(t, string(errBytes[:n]), "hello stderr")
}

func TestExecRunTerminalHandler_DispatchesMatching(t *testing.T) {
	done := make(chan *events.RunEvent, 1)
	h := execRunTerminalHandler("foo", done)

	// Non-RunEvent data (LogLineEvent) → ignored
	h(events.Event{Data: events.LogLineEvent{}})
	select {
	case <-done:
		t.Fatal("expected no event for non-RunEvent data")
	default:
	}

	// RunEvent with wrong task → ignored
	h(events.Event{Data: events.RunEvent{Run: &model.Run{TaskName: "bar"}}})
	select {
	case <-done:
		t.Fatal("expected no event for wrong task")
	default:
	}

	// nil Run → ignored
	h(events.Event{Data: events.RunEvent{Run: nil}})
	select {
	case <-done:
		t.Fatal("expected no event for nil run")
	default:
	}

	// Matching event → delivered
	h(events.Event{Data: events.RunEvent{Run: &model.Run{TaskName: "foo"}}})
	select {
	case got := <-done:
		require.NotNil(t, got.Run)
		assert.Equal(t, "foo", got.Run.TaskName)
	default:
		t.Fatal("expected event to be delivered")
	}
}

func TestRunExec_DaemonFlagRequiresDaemon(t *testing.T) {
	origFlag := execFlags.Daemon
	execFlags.Daemon = true
	t.Cleanup(func() { execFlags.Daemon = origFlag })

	exitCode, err := runExec("anything", Flags{DataDir: t.TempDir()})
	assert.Equal(t, 0, exitCode)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--daemon was set")
}

func TestRunExec_StandaloneFlagForbidsDaemon(t *testing.T) {
	dir := t.TempDir()
	// Write the current PID — isDaemonRunning will see a live daemon.
	pidPath := filepath.Join(dir, "daemon.pid")
	require.NoError(t, os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o600))

	origFlag := execFlags.Standalone
	execFlags.Standalone = true
	t.Cleanup(func() { execFlags.Standalone = origFlag })

	exitCode, err := runExec("anything", Flags{DataDir: dir})
	assert.Equal(t, 0, exitCode)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--standalone was set")
}

func TestRunExecStandalone_UnknownTaskName(t *testing.T) {
	// Write a minimal valid runwisp.toml that has no tasks named "missing".
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")
	const minimalCfg = `
[scheduler]
timezone = "UTC"

[tasks.exists]
cron = "* * * * *"
run = "echo hi"
`
	require.NoError(t, os.WriteFile(cfgPath, []byte(minimalCfg), 0o600))

	exitCode, err := runExecStandalone("missing", Flags{CfgFile: cfgPath})
	assert.Equal(t, 0, exitCode)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `task "missing" not found`)
}

func TestRunExecStandalone_BadConfigFile(t *testing.T) {
	t.Parallel()
	_, err := runExecStandalone("anything", Flags{CfgFile: "/does/not/exist/runwisp.toml"})
	require.Error(t, err)
}

func TestRunExecViaDaemon_DaemonUnreachable(t *testing.T) {
	t.Parallel()
	// No socket created — apiclient.NewUnix will fail HealthCheck.
	exitCode, err := runExecViaDaemon("anything", Flags{DataDir: t.TempDir()})
	assert.Equal(t, 0, exitCode)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "daemon is not reachable")
}

func TestRunExecStandalone_HappyPath_EchoTaskExitsZero(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns a subprocess")
	}

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")
	cfg := `
[scheduler]
timezone = "UTC"

[tasks.greet]
cron = "* * * * *"
run = "echo runwisp-exec-test"
`
	require.NoError(t, os.WriteFile(cfgPath, []byte(cfg), 0o600))

	exitCode, err := runExecStandalone("greet", Flags{CfgFile: cfgPath, DataDir: dir})
	require.NoError(t, err)
	assert.Equal(t, 0, exitCode)
}

func TestRunExecStandalone_InvalidConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("not valid toml ============="), 0o600))

	_, err := runExecStandalone("x", Flags{CfgFile: cfgPath})
	require.Error(t, err)
}

func TestExecRunTerminalHandler_ConcurrentSafe(t *testing.T) {
	done := make(chan *events.RunEvent, 10)
	h := execRunTerminalHandler("t", done)

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h(events.Event{Data: events.RunEvent{Run: &model.Run{TaskName: "t"}}})
		}()
	}
	wg.Wait()
	assert.Len(t, done, 5)
}
