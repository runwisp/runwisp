//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/require"
)

// TestPasswordCmd_PrintsEphemeralValue boots a daemon with no
// RUNWISP_PASSWORD set so the daemon mints an ephemeral one in memory, then
// invokes the built `runwisp password` binary against the same data dir and
// asserts the printed value matches what the socket-mediated API returns.
func TestPasswordCmd_PrintsEphemeralValue(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	configDir := t.TempDir()
	configPath := writeE2EConfig(t, configDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	creds, err := socketClient(t, daemon.dataDir).GetLocalCredentials()
	require.NoError(t, err)
	require.NotEmpty(t, creds.Password)
	require.True(t, creds.Ephemeral)

	stdout, stderr, exitCode := runPasswordCmd(t, binaryPath, projectDir, daemon.dataDir, daemon.port, nil)
	require.Equalf(t, 0, exitCode,
		"`runwisp password` should exit 0 in the ephemeral case; stderr:\n%s", stderr)
	require.Equal(t, creds.Password+"\n", stdout,
		"stdout should contain exactly the password and a trailing newline")
	require.Empty(t, stderr, "stderr should be silent on success")
}

// TestPasswordCmd_RefusesEnvVarPassword starts a daemon with
// RUNWISP_PASSWORD set, then invokes `runwisp password`. The daemon's
// endpoint must refuse to disclose the operator-supplied value (404), and
// the CLI must surface that as a non-zero exit with the refusal copy on
// stderr — never printing the value.
func TestPasswordCmd_RefusesEnvVarPassword(t *testing.T) {
	const operatorPassword = "operator-supplied-do-not-leak"

	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	configDir := t.TempDir()
	configPath := writeE2EConfig(t, configDir)
	daemon := startDaemonWithEnv(t, projectDir, binaryPath, configPath,
		"RUNWISP_PASSWORD="+operatorPassword,
	)

	stdout, stderr, exitCode := runPasswordCmd(t, binaryPath, projectDir, daemon.dataDir, daemon.port, nil)
	require.NotZero(t, exitCode, "env-var case must not exit 0")
	require.Empty(t, stdout,
		"`runwisp password` MUST NOT print any value when the daemon refuses disclosure")
	require.Contains(t, stderr, "RUNWISP_PASSWORD",
		"stderr should explain that the env-var value is not disclosed")
	require.NotContains(t, stdout+stderr, operatorPassword,
		"the operator-supplied password must NEVER leak via stdout or stderr")
}

// runPasswordCmd runs `runwisp --data <dir> --port <port> password` and
// returns captured stdout, stderr, and the process exit code.
func runPasswordCmd(
	t *testing.T,
	binaryPath, projectDir, dataDir string,
	port int,
	extraEnv []string,
) (string, string, int) {
	t.Helper()

	cmd := exec.Command(
		binaryPath,
		"--data", dataDir,
		"--port", strconv.Itoa(port),
		"password",
	)
	cmd.Dir = projectDir
	cmd.Env = subprocEnv(append([]string{"TERM=xterm-256color"}, extraEnv...)...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		} else {
			t.Fatalf("failed to run `runwisp password`: %v\nstderr:\n%s", err, stderr.String())
		}
	}
	return stdout.String(), stderr.String(), exitCode
}

// startDaemonWithEnv is the env-aware sibling of startDaemon. The standard
// helper grabs os.Environ() and ignores the test's per-case overrides, so we
// replicate just enough of it here to inject RUNWISP_PASSWORD without
// touching the existing flow used by every other suite.
func startDaemonWithEnv(t *testing.T, projectDir, binaryPath, configPath string, extraEnv ...string) *daemonProcess {
	t.Helper()

	dataDir := testutil.ShortTempDir(t)
	port := reserveTCPPort(t)
	baseURL := "http://127.0.0.1:" + strconv.Itoa(port)

	output := &lockedBuffer{}
	cmd := exec.Command(
		binaryPath,
		"--config", configPath,
		"--data", dataDir,
		"--port", strconv.Itoa(port),
		"daemon",
	)
	cmd.Dir = projectDir
	cmd.Env = subprocEnv(append([]string{"TERM=xterm-256color"}, extraEnv...)...)
	cmd.Stdout = output
	cmd.Stderr = output
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	require.NoError(t, cmd.Start())

	process := &daemonProcess{
		baseURL:  baseURL,
		dataDir:  dataDir,
		port:     port,
		cmd:      cmd,
		output:   output,
		waitDone: make(chan struct{}),
	}

	go func() {
		process.waitErr = cmd.Wait()
		close(process.waitDone)
	}()

	t.Cleanup(func() { process.stop(t) })

	deadline := time.Now().Add(10 * time.Second)
	socketPath := filepath.Join(dataDir, "runwisp.sock")
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(socketPath); statErr == nil {
			return process
		}
		if process.exited() {
			require.FailNowf(t, "daemon exited before socket appeared", "output:\n%s", output.Tail(16_000))
		}
		time.Sleep(screenPollInterval)
	}
	require.FailNowf(t, "daemon did not create socket within timeout", "output:\n%s", output.Tail(16_000))
	return nil
}
