//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMultilineRunStopsAtFirstFailure proves the fail-fast guarantee holds
// across the whole stack — TOML, config load, executor argv, run status, and
// the captured log all have to agree. Before RunWisp armed errexit, this run
// finished `success` with exit 0 because the shell ran on past the failing line
// and the run inherited the *last* command's status: a broken script recorded
// as a good one, which is the failure mode the daemon exists to prevent.
//
// The opt-out is exercised in the same daemon: an otherwise identical task
// whose first line is `set +e` must still finish successfully, since that is
// what makes a dedicated TOML key unnecessary.
func TestMultilineRunStopsAtFirstFailure(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
[daemon]
shutdown_timeout = "500ms"

[tasks.failfast]
run = """
echo before
nonexistent_runwisp_command
echo after
"""

[tasks.optout]
run = """
set +e
nonexistent_runwisp_command
echo after
"""
`), 0o600))

	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)
	client := socketClient(t, daemon.dataDir)

	t.Run("a failing line fails the run", func(t *testing.T) {
		_, err := client.TriggerRun("failfast", nil)
		require.NoError(t, err)
		waitForRunCount(t, client, "failfast", 1, 10*time.Second)

		run := waitForEndedRun(t, client, "failfast")
		assert.Equal(t, 127, run.ExitCode, "the run carries the failing command's exit code")
		require.NotNil(t, run.EndReason)
		assert.Equal(t, model.ReasonFailed, *run.EndReason)

		logBody, err := client.GetLogPage(run.ID, 0, 100)
		require.NoError(t, err)
		joined := joinLogLines(logBody.Lines)
		assert.Contains(t, joined, "before", "output before the failure is still captured")
		assert.NotContains(t, joined, "after", "execution stops at the first failing command")
	})

	t.Run("set +e opts out", func(t *testing.T) {
		_, err := client.TriggerRun("optout", nil)
		require.NoError(t, err)
		waitForRunCount(t, client, "optout", 1, 10*time.Second)

		run := waitForEndedRun(t, client, "optout")
		assert.Equal(t, 0, run.ExitCode, "set +e restores continue-on-error")

		logBody, err := client.GetLogPage(run.ID, 0, 100)
		require.NoError(t, err)
		assert.Contains(t, joinLogLines(logBody.Lines), "after")
	})
}

// joinLogLines flattens a log page's committed lines into one searchable blob.
func joinLogLines(lines []server.LogLineEntry) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

// waitForEndedRun polls until the task's most recent run has reached a terminal
// phase, so assertions never race the run manager's final write.
func waitForEndedRun(t testing.TB, client *apiclient.Client, taskName string) model.Run {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		runs, _, err := client.ListRunsByTask(taskName, apiclient.RunsParams{Limit: 1})
		require.NoError(t, err)
		if len(runs) > 0 && runs[0].Status == model.PhaseEnded {
			return runs[0]
		}
		time.Sleep(screenPollInterval)
	}

	require.FailNowf(t, "run never reached a terminal phase", "task: %s", taskName)
	return model.Run{}
}
