//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/hinshun/vt10x"
	"github.com/stretchr/testify/require"
)

// TestSpawnedDaemonNotServiceManaged guards the systemd-detection leak: a daemon
// that runwisp spawns itself must never self-report as service-managed just
// because the launching shell carried INVOCATION_ID (set by systemd for every
// unit, including the terminal emulator). The marker is visible in the quit
// dialog — a falsely-managed daemon drops the "Shut Down" option and only offers
// "Quit TUI" with a `runwisp stop` hint.
func TestSpawnedDaemonNotServiceManaged(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	configDir := t.TempDir()
	configPath := writeE2EConfig(t, configDir)
	dataDir := t.TempDir()
	port := reserveTCPPort(t)

	// The root command spawns a detached background daemon, which the TUI's
	// process group can't reach — stop it explicitly however the test ends.
	t.Cleanup(func() {
		_, _ = runCLI(t, projectDir, binaryPath, "stop", "--data", dataDir, "--config", configPath)
	})

	session := startLocalTUI(t, projectDir, binaryPath, configPath, dataDir, port,
		// Simulate a systemd-scoped terminal leaking INVOCATION_ID into the
		// environment runwisp spawns the daemon from.
		"INVOCATION_ID=5d10ec423bcf449789f2dfd36760a4ab")

	session.press(t, "q")
	// With the leak fixed the daemon reports ServiceManaged=false, so the quit
	// dialog offers the standard keep/shutdown choice. Before the fix it would
	// show "Quit TUI" + a `runwisp stop` hint and this wait would time out.
	session.waitForAll(t, 5*time.Second, "Quit", "Keep Running", "Shut Down")
}

// startLocalTUI launches the root `runwisp` command (no subcommand) in a PTY
// against a caller-chosen data dir and port. Unlike startRemoteTUI it does not
// expect a pre-started daemon — the root command spawns its own detached daemon
// (via spawnDaemonProcess) and then attaches the TUI. extraEnv entries are
// appended verbatim, surviving subprocEnv's marker-var stripping.
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
