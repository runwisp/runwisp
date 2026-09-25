//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/require"
)

// TestCLIControlTargetsService exercises `runwisp start|stop|restart <name>`
// against a service: start un-parks a stopped service, a second start is a
// no-op (no second instance), and restart bounces the running instance.
func TestCLIControlTargetsService(t *testing.T) {
	t.Setenv("RUNWISP_AUTH", "off")
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "runwisp.toml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
[daemon]
shutdown_timeout = "500ms"

[services.svc]
run = "sleep 100"
autostart = false
graceful_stop = "0s"
`), 0o600))

	daemon := startDaemon(t, projectDir, binaryPath, configPath)
	client := apiclient.New(daemon.baseURL, "")

	require.Empty(t, activeRuns(t, client, "svc"), "autostart=false must not start an instance")

	out, err := runCLI(t, projectDir, binaryPath, "start", "svc", "--data", daemon.dataDir, "--config", configPath)
	require.NoError(t, err, "start should succeed: %s", out)
	require.Contains(t, out, `Service "svc" started.`)
	require.Eventually(t, func() bool { return len(activeRuns(t, client, "svc")) == 1 },
		5*time.Second, 100*time.Millisecond, "service should have exactly one running instance")
	firstRunID := activeRuns(t, client, "svc")[0].ID

	out, err = runCLI(t, projectDir, binaryPath, "start", "svc", "--data", daemon.dataDir, "--config", configPath)
	require.NoError(t, err, "second start should succeed: %s", out)
	require.Len(t, activeRuns(t, client, "svc"), 1, "starting an already-running service must not add a second instance")

	out, err = runCLI(t, projectDir, binaryPath, "restart", "svc", "--data", daemon.dataDir, "--config", configPath)
	require.NoError(t, err, "restart should succeed: %s", out)
	require.Contains(t, out, `Service "svc" restarted.`)
	require.Eventually(t, func() bool {
		active := activeRuns(t, client, "svc")
		return len(active) == 1 && active[0].ID != firstRunID
	}, 5*time.Second, 100*time.Millisecond, "restart should bounce the instance to a new run")

	out, err = runCLI(t, projectDir, binaryPath, "stop", "svc", "--data", daemon.dataDir, "--config", configPath)
	require.NoError(t, err, "stop should succeed: %s", out)
	require.Contains(t, out, `Service "svc" stopped.`)
	require.Eventually(t, func() bool { return len(activeRuns(t, client, "svc")) == 0 },
		5*time.Second, 100*time.Millisecond, "stop should leave no running instance")
}

// TestCLIControlTargetsTask exercises `runwisp start|stop|restart <name>`
// against a plain task: start triggers a run and a second start is a no-op
// while one is active, restart cancels and re-triggers exactly one fresh run,
// and a multi-target stop that includes a manual_trigger=false task reports a
// partial failure while still stopping the others.
func TestCLIControlTargetsTask(t *testing.T) {
	t.Setenv("RUNWISP_AUTH", "off")
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "runwisp.toml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
[daemon]
shutdown_timeout = "500ms"

[tasks.longtask]
run = "sleep 100"
graceful_stop = "0s"

[tasks.locked]
run = "sleep 100"
manual_trigger = false
`), 0o600))

	daemon := startDaemon(t, projectDir, binaryPath, configPath)
	client := apiclient.New(daemon.baseURL, "")

	out, err := runCLI(t, projectDir, binaryPath, "start", "longtask", "--data", daemon.dataDir, "--config", configPath)
	require.NoError(t, err, "start should succeed: %s", out)
	require.Contains(t, out, `Task "longtask" started.`)
	require.Eventually(t, func() bool { return len(activeRuns(t, client, "longtask")) == 1 },
		5*time.Second, 100*time.Millisecond, "start should trigger exactly one run")
	firstRunID := activeRuns(t, client, "longtask")[0].ID

	out, err = runCLI(t, projectDir, binaryPath, "start", "longtask", "--data", daemon.dataDir, "--config", configPath)
	require.NoError(t, err, "second start should succeed: %s", out)
	require.Len(t, activeRuns(t, client, "longtask"), 1, "starting a task with an active run must be a no-op")

	out, err = runCLI(t, projectDir, binaryPath, "restart", "longtask", "--data", daemon.dataDir, "--config", configPath)
	require.NoError(t, err, "restart should succeed: %s", out)
	require.Contains(t, out, `Task "longtask" restarted.`)
	require.Eventually(t, func() bool {
		active := activeRuns(t, client, "longtask")
		return len(active) == 1 && active[0].ID != firstRunID
	}, 5*time.Second, 100*time.Millisecond, "restart should cancel the run and trigger exactly one fresh one")

	out, err = runCLI(t, projectDir, binaryPath, "stop", "longtask", "locked", "--data", daemon.dataDir, "--config", configPath)
	require.Error(t, err, "stop must fail overall when one target is locked: %s", out)
	require.Contains(t, out, `Task "longtask" stopped.`)
	require.Contains(t, out, `locked: cannot stop "locked": manual_trigger = false in runwisp.toml`)
	require.Contains(t, out, "1 of 2 targets failed")
	require.Eventually(t, func() bool { return len(activeRuns(t, client, "longtask")) == 0 },
		5*time.Second, 100*time.Millisecond, "the unlocked target must still be stopped")
}

// activeRuns lists the running (or pending) runs for taskName, used to assert
// how many instances/runs a service or task currently has in flight.
func activeRuns(t *testing.T, client *apiclient.Client, taskName string) []model.Run {
	t.Helper()
	runs, _, err := client.ListRunsByTask(t.Context(), taskName, apiclient.RunsParams{Status: "running"})
	require.NoError(t, err)
	return runs
}
