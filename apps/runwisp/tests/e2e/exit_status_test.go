//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A daemon that dies of a fatal error must exit non-zero, or systemd's
// Restart=on-failure never restarts it and a take-over's OnFailure= failsafe
// never hands the jobs back to cron. An over-long data dir makes the unix
// socket bind fail inside the running server (sun_path is ~108 bytes), which
// is the self-triggered teardown path; it used to finish with exit 0.
func TestDaemonExitStatus_FatalServerErrorExitsNonZero(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	configPath := writeE2EConfig(t, t.TempDir())
	dataDir := filepath.Join(t.TempDir(), strings.Repeat("d", 110))
	require.NoError(t, os.MkdirAll(dataDir, 0o700))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath,
		"--config", configPath,
		"--data", dataDir,
		"--port", strconv.Itoa(reserveTCPPort(t)),
		"daemon",
	)
	cmd.Dir = projectDir
	cmd.Env = subprocEnv("TERM=dumb")
	out, err := cmd.CombinedOutput()

	var exitErr *exec.ExitError
	require.True(t, errors.As(err, &exitErr), "daemon must exit non-zero, got %v\noutput:\n%s", err, out)
	assert.Equal(t, 1, exitErr.ExitCode(), "output:\n%s", out)
	assert.Contains(t, string(out), "will exit non-zero")
}

// An operator's SIGTERM (`runwisp stop`, `systemctl stop`) is a clean stop and
// must stay exit 0, so a service manager does not treat it as a failure.
func TestDaemonExitStatus_SignalExitsZero(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	daemon := startDaemon(t, projectDir, binaryPath, writeE2EConfig(t, t.TempDir()))

	require.NoError(t, daemon.cmd.Process.Signal(syscall.SIGTERM))
	require.True(t, daemon.waitForExit(processExitTimeout), "daemon did not exit on SIGTERM")
	assert.NoError(t, daemon.waitErr, "output:\n%s", daemon.output.Tail(4000))
}
