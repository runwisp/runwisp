//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"encoding/json"
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

// TestGoldenPathFireAppearsInAPILogAndSSE is the end-to-end smoke test that
// guards the daemon's prime directive: every run produces a persisted record,
// a non-empty log file on disk, and an SSE notification — all reachable from a
// fresh boot with a one-task TOML.
//
// It spins up a real daemon binary against a tempdir data root, subscribes to
// the run-events SSE stream BEFORE triggering the task so we observe the
// `run.created` event live, then asserts:
//  1. The fired run shows up in GET /api/runs with a real ULID.
//  2. The log file under <dataDir>/logs/<task>/ is non-empty.
//  3. The SSE subscriber received a `run.created` envelope for that run.
//
// The task is triggered via the REST API rather than a cron expression so the
// test converges in milliseconds and avoids a minute-aligned wait.
func TestGoldenPathFireAppearsInAPILogAndSSE(t *testing.T) {
	const taskName = "golden-task"

	configPath := writeGoldenConfig(t, taskName)
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	client := socketClient(t, daemon.dataDir)

	streamCtx, cancelStream := context.WithCancel(context.Background())
	t.Cleanup(cancelStream)
	stream, err := client.StreamRunEvents(streamCtx)
	require.NoError(t, err, "StreamRunEvents should connect")

	// Drain the initial keepalive ping the server emits to flush response
	// headers so any subsequent event is a real lifecycle event.
	waitForRunStreamPing(t, stream, 5*time.Second)

	triggered, err := client.TriggerRun(taskName)
	require.NoError(t, err, "TriggerRun should succeed")
	require.NotEmpty(t, triggered.ID)

	created := waitForRunCreatedEvent(t, stream, taskName, 10*time.Second)
	require.Equal(t, triggered.ID, created.ID,
		"run.created event id should match the id returned from TriggerRun")
	require.Equal(t, taskName, created.TaskName)

	run := waitForListedRun(t, client, taskName, created.ID, 5*time.Second)

	logDir := filepath.Join(daemon.dataDir, "logs")
	logPath := logutil.ResolveRunLogPath(logDir, run.TaskName, run.ID, run.CreatedAt)

	requireLogFileNonEmpty(t, logPath, 10*time.Second)
}

// writeGoldenConfig writes a one-task TOML with no cron expression: the task
// is API-triggered only, which keeps the test fast and deterministic.
func writeGoldenConfig(t *testing.T, taskName string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "runwisp.golden.toml")
	body := fmt.Sprintf(`
[daemon]
shutdown_timeout = "500ms"

[tasks.%s]
run = "echo golden-path-ok"
`, taskName)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// waitForRunStreamPing blocks until the initial flush-the-headers ping arrives
// on the run-events SSE channel, so subsequent events are real lifecycle events.
func waitForRunStreamPing(t *testing.T, stream <-chan apiclient.RunStreamEvent, timeout time.Duration) {
	t.Helper()
	select {
	case ev, ok := <-stream:
		require.True(t, ok, "run stream closed before any event arrived")
		require.Equal(t, "ping", ev.Type, "first SSE message should be the keepalive ping")
	case <-time.After(timeout):
		t.Fatalf("never received initial SSE ping on run stream within %s", timeout)
	}
}

// waitForRunCreatedEvent drains the SSE channel until a `run.created` event
// for the named task arrives. Other event types (pings, late updates) are
// ignored. Failure includes the elapsed timeout so triage doesn't need a
// stopwatch.
func waitForRunCreatedEvent(
	t *testing.T,
	stream <-chan apiclient.RunStreamEvent,
	taskName string,
	timeout time.Duration,
) model.Run {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-stream:
			if !ok {
				t.Fatalf("SSE run stream closed before %q event arrived", "run.created")
			}
			if ev.Type != "run.created" {
				continue
			}
			var envelope struct {
				Run *model.Run `json:"run"`
			}
			require.NoError(t, json.Unmarshal(ev.Data, &envelope), "decode run.created envelope")
			require.NotNil(t, envelope.Run, "run.created envelope must carry a non-nil run")
			if envelope.Run.TaskName != taskName {
				continue
			}
			return *envelope.Run
		case <-deadline:
			t.Fatalf("never received a run.created event for %q within %s", taskName, timeout)
		}
	}
}

// waitForListedRun polls GET /api/runs until the run with the expected id
// (just published via SSE) shows up — there can be a brief gap between the
// event fan-out and the storage write becoming queryable, so we poll instead
// of asserting a single read.
func waitForListedRun(
	t *testing.T,
	client *apiclient.Client,
	taskName, runID string,
	timeout time.Duration,
) model.Run {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		runs, _, err := client.ListRuns(apiclient.RunsParams{Limit: 50})
		require.NoError(t, err)
		for _, run := range runs {
			if run.ID == runID {
				require.Equal(t, taskName, run.TaskName)
				return run
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("run %s for %s never showed up in GET /api/runs within %s", runID, taskName, timeout)
	return model.Run{}
}

// requireLogFileNonEmpty waits for the on-disk log file to materialize and
// contain at least one byte. The executor opens the log file lazily — there
// is a short window between `run.created` and the first write — so we poll.
func requireLogFileNonEmpty(t *testing.T, logPath string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		info, err := os.Stat(logPath)
		if err == nil && info.Size() > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("log file %s never became non-empty within %s", logPath, timeout)
}
