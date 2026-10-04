//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/require"
)

// TestPausedScheduleSurvivesRestartWithoutMissedRuns boots a daemon over a
// data dir where "tick" was paused while the daemon was down. The pause must
// come back from SQLite, and the 15 ticks inside the paused window must not be
// recorded as missed, neither at boot nor when `runwisp resume` lifts it.
func TestPausedScheduleSurvivesRestartWithoutMissedRuns(t *testing.T) {
	t.Parallel()
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	configPath := writeNotifyConfig(t, `
[tasks.tick]
cron = "* * * * *"
catch_up = 0
run = "true"
`)
	dataDir := testutil.ShortTempDir(t)
	port := reserveTCPPort(t)
	seedAnchorRun(t, dataDir, "tick", -15*time.Minute)
	seedPause(t, dataDir, "tick", -14*time.Minute)

	daemon := startDaemonOn(t, projectDir, binaryPath, configPath, dataDir, port)
	client := socketClient(t, daemon.dataDir)
	daemon.waitForReady(t, client, 10*time.Second)

	tick := taskByName(t, client, "tick")
	require.NotNil(t, tick.PausedAt, "the pause is restored from SQLite at boot")
	require.Nil(t, tick.NextRunAt, "a paused task has no next run")
	requireNoMissedRuns(t, client, "the paused window must not be caught up at boot")

	out, err := runCLI(t, projectDir, binaryPath, "resume", "tick", "--data", dataDir, "--config", configPath)
	require.NoError(t, err, "resume should succeed: %s", out)
	require.Contains(t, out, `Task "tick" resumed.`)

	tick = taskByName(t, client, "tick")
	require.Nil(t, tick.PausedAt)
	require.NotNil(t, tick.NextRunAt, "a resumed task is scheduled again")
	requireNoMissedRuns(t, client, "resuming must not catch up the paused window")
}

// TestPauseStopsCronFiring pauses a fast `@every` task through the CLI and
// checks that it records no further runs, while a manual run still works.
func TestPauseStopsCronFiring(t *testing.T) {
	t.Parallel()
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	configPath := writeNotifyConfig(t, `
[tasks.fast]
cron = "@every 1s"
run = "true"
`)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)
	client := socketClient(t, daemon.dataDir)

	require.Eventually(t, func() bool { n, ok := runCount(client, "fast"); return ok && n > 0 },
		10*time.Second, 100*time.Millisecond, "the task should fire before it is paused")

	out, err := runCLI(t, projectDir, binaryPath, "pause", "*", "--data", daemon.dataDir, "--config", configPath)
	require.NoError(t, err, "pause should succeed: %s", out)
	require.Contains(t, out, `Task "fast" paused.`)

	// A tick already past the pause check may still land; let it settle.
	time.Sleep(1500 * time.Millisecond)
	settled, ok := runCount(client, "fast")
	require.True(t, ok)
	require.Never(t, func() bool { n, ok := runCount(client, "fast"); return ok && n != settled },
		3*time.Second, 200*time.Millisecond, "a paused schedule must not fire")

	_, err = client.TriggerRun(t.Context(), "fast", nil, "")
	require.NoError(t, err, "manual runs keep working while paused")
	require.Eventually(t, func() bool { n, ok := runCount(client, "fast"); return ok && n == settled+1 },
		5*time.Second, 100*time.Millisecond)
}

// seedPause writes a schedule pause straight into the data dir before boot,
// dated by offset (negative = in the past).
func seedPause(t *testing.T, dataDir, taskName string, offset time.Duration) {
	t.Helper()
	db, err := storage.New(filepath.Join(dataDir, "runwisp.db"))
	require.NoError(t, err)
	defer func() { require.NoError(t, db.Close()) }()
	require.NoError(t, db.PauseTaskSchedule(context.Background(), taskName, time.Now().Add(offset)))
}

func taskByName(t *testing.T, client *apiclient.Client, name string) model.TaskResponse {
	t.Helper()
	tasks, err := client.ListTasks(t.Context())
	require.NoError(t, err)
	for _, task := range tasks {
		if task.Name == name {
			return task
		}
	}
	t.Fatalf("task %q not in the daemon's task list", name)
	return model.TaskResponse{}
}

func requireNoMissedRuns(t *testing.T, client *apiclient.Client, msg string) {
	t.Helper()
	require.Never(t, func() bool {
		s, err := client.GetRunSummary(context.Background())
		return err == nil && s.Missed != 0
	}, 2*time.Second, 200*time.Millisecond, msg)
}

// runCount is polled from require.Eventually/Never, whose condition goroutines
// can outlive the check and the test, so it must not fail the test or use
// t.Context(). ok is false when the request failed.
func runCount(client *apiclient.Client, taskName string) (total int64, ok bool) {
	_, total, err := client.ListRunsByTask(context.Background(), taskName, apiclient.RunsParams{Limit: 1})
	return total, err == nil
}
