//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"strconv"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/stretchr/testify/require"
)

// TestCLIStopRestart covers the non-service path of `runwisp restart` and
// `runwisp stop`: restart SIGTERMs the running daemon and spawns a fresh one
// on the same port; stop brings it down and a second stop is a friendly no-op.
func TestCLIStopRestart(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	configDir := t.TempDir()
	configPath := writeE2EConfig(t, configDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	// The daemon `restart` respawns is detached from the harness process
	// group, so the startDaemon cleanup can't reach it — stop it ourselves
	// no matter how the assertions below play out.
	t.Cleanup(func() {
		_, _ = runCLI(t, projectDir, binaryPath, "stop", "--data", daemon.dataDir, "--config", configPath)
	})

	out, err := runCLI(t, projectDir, binaryPath,
		"restart",
		"--data", daemon.dataDir,
		"--config", configPath,
		"--port", strconv.Itoa(daemon.port),
	)
	require.NoError(t, err, "restart should succeed: %s", out)
	require.Contains(t, out, "Daemon restarted")

	// The original daemon process must have exited...
	require.True(t, daemon.waitForExit(processExitTimeout), "old daemon should exit on restart")

	// ...and a fresh one must answer on the same port.
	client := apiclient.New(daemon.baseURL, "")
	require.Eventually(t, func() bool { return client.HealthCheck() == nil },
		10*time.Second, 100*time.Millisecond, "restarted daemon should pass health checks")

	out, err = runCLI(t, projectDir, binaryPath,
		"stop", "--data", daemon.dataDir, "--config", configPath)
	require.NoError(t, err, "stop should succeed: %s", out)
	require.Contains(t, out, "Daemon stopped")

	require.Eventually(t, func() bool { return client.HealthCheck() != nil },
		10*time.Second, 100*time.Millisecond, "stopped daemon should stop answering")

	// Stopping again is not an error — just a friendly message.
	out, err = runCLI(t, projectDir, binaryPath,
		"stop", "--data", daemon.dataDir, "--config", configPath)
	require.NoError(t, err, "stop with no daemon should exit 0: %s", out)
	require.Contains(t, out, "nothing to stop")
}
