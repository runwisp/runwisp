//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"strconv"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/apps/runwisp/internal/datadir"
	"github.com/stretchr/testify/require"
)

// TestCLIStopRestart covers the non-service path of `runwisp restart` and
// `runwisp stop`: restart brings the daemon back in place, as the same process
// (so a container or terminal running it keeps it); stop brings it down and a
// second stop is a friendly no-op.
func TestCLIStopRestart(t *testing.T) {
	t.Parallel()
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	configDir := t.TempDir()
	configPath := writeE2EConfig(t, configDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	sock := apiclient.NewUnix(datadir.SocketPath(daemon.dataDir))
	before, err := sock.GetDaemonInfo(t.Context())
	require.NoError(t, err)

	out, err := runCLI(t, projectDir, binaryPath,
		"restart",
		"--data", daemon.dataDir,
		"--config", configPath,
		"--port", strconv.Itoa(daemon.port),
	)
	require.NoError(t, err, "restart should succeed: %s", out)
	require.Contains(t, out, "Daemon restarted")

	select {
	case <-daemon.waitDone:
		t.Fatalf("restart in place must keep the daemon process running:\n%s", daemon.output.Tail(4000))
	default:
	}
	after, err := sock.GetDaemonInfo(t.Context())
	require.NoError(t, err)
	require.True(t, after.StartedAt.After(before.StartedAt), "the daemon should have started again")

	client := apiclient.New(daemon.baseURL, "")
	require.NoError(t, client.HealthCheck(t.Context()), "the restarted daemon serves HTTP again")

	out, err = runCLI(t, projectDir, binaryPath,
		"stop", "--data", daemon.dataDir, "--config", configPath)
	require.NoError(t, err, "stop should succeed: %s", out)
	require.Contains(t, out, "Daemon stopped")

	require.Eventually(t, func() bool { return client.HealthCheck(t.Context()) != nil },
		10*time.Second, 100*time.Millisecond, "stopped daemon should stop answering")

	// Stopping again is not an error — just a friendly message.
	out, err = runCLI(t, projectDir, binaryPath,
		"stop", "--data", daemon.dataDir, "--config", configPath)
	require.NoError(t, err, "stop with no daemon should exit 0: %s", out)
	require.Contains(t, out, "nothing to stop")
}
