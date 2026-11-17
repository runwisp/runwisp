//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
	"github.com/runwisp/runwisp/apps/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/apps/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/apps/runwisp/internal/testutil"
	"github.com/stretchr/testify/require"
)

// TestRootTUIQuitStopsItsDaemon: bare `runwisp` runs the daemon in its own
// process, so quitting the TUI with nothing running stops it right away and
// leaves no daemon behind. It used to spawn a detached daemon that outlived
// the terminal.
func TestRootTUIQuitStopsItsDaemon(t *testing.T) {
	t.Parallel()
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	configPath := writeE2EConfig(t, t.TempDir())
	dataDir := testutil.ShortTempDir(t)
	port := reserveTCPPort(t)

	session := startLocalTUI(t, projectDir, binaryPath, configPath, dataDir, port)
	session.press(t, "q")
	requireExitedWithDaemon(t, session, port)
}

// TestRootTUIQuitConfirmsBeforeInterruptingRuns: with a task run in flight,
// quitting names it and only stops RunWisp once confirmed.
func TestRootTUIQuitConfirmsBeforeInterruptingRuns(t *testing.T) {
	t.Parallel()
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	configPath := filepath.Join(t.TempDir(), "runwisp.toml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
[daemon]
shutdown_timeout = "500ms"

[tasks.sleeper]
run = "sleep 60"
`), 0o600))
	dataDir := testutil.ShortTempDir(t)
	port := reserveTCPPort(t)

	session := startLocalTUI(t, projectDir, binaryPath, configPath, dataDir, port)
	out, err := runCLI(t, projectDir, binaryPath, "start", "sleeper", "--data", dataDir, "--config", configPath)
	require.NoError(t, err, "trigger sleeper: %s", out)
	require.Eventually(t, func() bool {
		runs, _, err := apiclient.NewUnix(datadir.SocketPath(dataDir)).
			ListRuns(t.Context(), apiclient.RunsParams{Status: "running", TaskName: "sleeper"})
		return err == nil && len(runs) > 0
	}, 10*time.Second, 100*time.Millisecond, "sleeper should be running")

	session.press(t, "q")
	session.waitForAll(t, 5*time.Second, "Quitting stops RunWisp and interrupts", "sleeper.")
	require.False(t, session.exited(), "quit must wait for the confirm")

	session.press(t, "y")
	requireExitedWithDaemon(t, session, port)
}

// TestRootTUIRestartInPlace: `runwisp restart` against the daemon a TUI runs
// closes the TUI, restarts the daemon in the same process and opens the TUI
// again in the same terminal.
func TestRootTUIRestartInPlace(t *testing.T) {
	t.Parallel()
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	configPath := writeE2EConfig(t, t.TempDir())
	dataDir := testutil.ShortTempDir(t)
	port := reserveTCPPort(t)

	session := startLocalTUI(t, projectDir, binaryPath, configPath, dataDir, port)
	sock := apiclient.NewUnix(datadir.SocketPath(dataDir))
	before, err := sock.GetDaemonInfo(t.Context())
	require.NoError(t, err)

	out, err := runCLI(t, projectDir, binaryPath, "restart", "--data", dataDir, "--config", configPath)
	require.NoError(t, err, "restart: %s", out)
	require.False(t, session.exited(), "restart in place keeps the TUI's process")
	after, err := sock.GetDaemonInfo(t.Context())
	require.NoError(t, err)
	require.True(t, after.StartedAt.After(before.StartedAt), "the daemon should have started again")
	session.waitForAll(t, 15*time.Second, "Home", "Open Web UI")

	session.press(t, "q")
	requireExitedWithDaemon(t, session, port)
}

// requireExitedWithDaemon waits for the root TUI process to exit and checks
// its daemon went with it.
func requireExitedWithDaemon(t *testing.T, s *tuiSession, port int) {
	t.Helper()
	select {
	case <-s.waitDone:
	case <-time.After(processExitTimeout):
		require.FailNowf(t, "runwisp did not exit after quit", "raw output:\n%s", s.output.Tail(16_000))
	}
	client := apiclient.New("http://127.0.0.1:"+strconv.Itoa(port), "")
	require.Error(t, client.HealthCheck(t.Context()), "quitting must stop the daemon, not leave it running")
}

// startLocalTUI launches the root `runwisp` command (no subcommand) in a PTY
// against a caller-chosen data dir and port. Unlike startRemoteTUI it does not
// expect a pre-started daemon: the root command runs its own in-process. The
// data dir should be a testutil.ShortTempDir, since the daemon's Unix socket
// lives there and macOS caps socket paths at 104 bytes. extraEnv entries are
// appended verbatim.
func startLocalTUI(t *testing.T, projectDir, binaryPath, configPath, dataDir string, port int, extraEnv ...string) *tuiSession {
	t.Helper()

	cmd := exec.Command(
		binaryPath,
		"--config", configPath,
		"--data", dataDir,
		"--port", strconv.Itoa(port),
	)
	cmd.Dir = projectDir
	cmd.Env = subprocEnv(append([]string{"TERM=dumb"}, extraEnv...)...)

	ptyFile, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: screenCols,
		Rows: screenRows,
	})
	require.NoError(t, err)

	session := &tuiSession{
		cmd:      cmd,
		ptyFile:  ptyFile,
		term:     vt10x.New(vt10x.WithSize(screenCols, screenRows)),
		output:   &lockedBuffer{},
		waitDone: make(chan struct{}),
	}

	go func() {
		session.waitErr = cmd.Wait()
		close(session.waitDone)
	}()

	go session.readOutput()

	t.Cleanup(func() {
		session.forceStop()
	})

	session.waitForAll(t, 15*time.Second,
		"Home",
		"Web UI",
		"Open Web UI",
	)

	return session
}
