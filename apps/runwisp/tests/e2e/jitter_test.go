//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/require"
)

// TestJitterCronTaskFiresAndIsBrowsable proves, against the real binary, that
// adding a bare `jitter` to a cron task neither breaks scheduling nor hides
// runs: a jittered `@every` task still fires real cron runs through the
// work-conserving gate, and each run stays fully observable — recorded as a
// cron trigger, ending in success, with its stdout captured on disk. This is
// the prime-directive guard (nothing silently fails) plus the
// scheduler → gate → manager wiring smoke test the unit suite can't reach.
//
// The gate is idle, so the work-conserving path pulls each firing forward to
// its tick with no jitter delay; the test converges in a couple of ticks rather
// than idling out the window. `@every 2s` keeps the live gap above one second
// so a real, non-zero jitter window is computed and threaded through to the
// gate (rather than clamping to zero).
func TestJitterCronTaskFiresAndIsBrowsable(t *testing.T) {
	const taskName = "jittered"

	configPath := writeJitterConfig(t, taskName)
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	client := socketClient(t, daemon.dataDir)

	run := waitForCronRun(t, client, taskName, 20*time.Second)
	require.Equal(t, model.TriggeredByCron, run.TriggeredBy,
		"a jittered firing must be recorded as a cron trigger")
	require.NotNil(t, run.EndReason)
	require.Equal(t, model.ReasonSuccess, *run.EndReason,
		"the jittered run must complete successfully")

	logDir := filepath.Join(daemon.dataDir, "logs")
	logPath := logutil.ResolveRunLogPath(logDir, run.TaskName, run.ID, run.CreatedAt)
	requireLogFileNonEmpty(t, logPath, 10*time.Second)
}

// writeJitterConfig writes a one-task TOML whose only scheduling surface beyond
// the cron is a bare `jitter` — the whole feature is that one knob.
func writeJitterConfig(t *testing.T, taskName string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "runwisp.jitter.toml")
	body := fmt.Sprintf(`
[daemon]
shutdown_timeout = "500ms"

[tasks.%s]
cron = "@every 2s"
jitter = "2s"
run = "echo jitter-ok"
`, taskName)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// waitForCronRun polls GET /api/runs until a terminal, cron-triggered run for
// the task shows up, returning it. It fails on timeout so a scheduling-wiring
// regression surfaces as a clear failure rather than a hang.
func waitForCronRun(t *testing.T, client *apiclient.Client, taskName string, timeout time.Duration) model.Run {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		runs, _, err := client.ListRuns(apiclient.RunsParams{Limit: 50})
		require.NoError(t, err)
		for _, run := range runs {
			if run.TaskName == taskName &&
				run.TriggeredBy == model.TriggeredByCron &&
				run.Status == model.PhaseEnded {
				return run
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no terminal cron run for %q showed up within %s", taskName, timeout)
	return model.Run{}
}
