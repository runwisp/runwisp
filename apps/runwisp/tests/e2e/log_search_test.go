//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/stretchr/testify/require"
)

// TestLogSearchEndpointFindsLine drives the new /api/tasks/{name}/log/search
// endpoint against a real daemon. The task is API-triggered (no cron) so the
// test converges in milliseconds, and the script prints a distinctive marker
// that the search query targets — passing the test means the on-demand scan
// found that marker in the on-disk log of a finished run.
func TestLogSearchEndpointFindsLine(t *testing.T) {
	const (
		taskName = "search-task"
		marker   = "connection refused: 0xCAFEBEEF"
	)

	configPath := writeSearchConfig(t, taskName, marker)
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	client := socketClient(t, daemon.dataDir)

	streamCtx, cancelStream := context.WithCancel(context.Background())
	t.Cleanup(cancelStream)
	stream, err := client.StreamRunEvents(streamCtx)
	require.NoError(t, err)
	waitForRunStreamPing(t, stream, 5*time.Second)

	triggered, err := client.TriggerRun(taskName, nil)
	require.NoError(t, err)
	require.NotEmpty(t, triggered.ID)

	// Wait for the run to finish so the log file is fully written to disk.
	waitForRunCompleted(t, stream, triggered.ID, 10*time.Second)

	// Substring search across all runs of the task.
	body, err := client.SearchLogs(taskName, apiclient.SearchLogsOptions{Query: marker})
	require.NoError(t, err)
	require.NotEmpty(t, body.Items, "expected at least one hit for the marker")
	hit := body.Items[0]
	require.Equal(t, triggered.ID, hit.RunID)
	require.Contains(t, hit.Text, marker)
	require.True(t, body.Exhausted)
	require.Empty(t, body.NextCursor)

	// Same query scoped to the specific run id — should match identically.
	scoped, err := client.SearchLogs(taskName, apiclient.SearchLogsOptions{
		Query: marker,
		RunID: triggered.ID,
	})
	require.NoError(t, err)
	require.Len(t, scoped.Items, 1)
	require.Equal(t, triggered.ID, scoped.Items[0].RunID)

	// A query that cannot match returns an empty page, not a 5xx.
	empty, err := client.SearchLogs(taskName, apiclient.SearchLogsOptions{Query: "absolutely-no-match"})
	require.NoError(t, err)
	require.Empty(t, empty.Items)
	require.True(t, empty.Exhausted)
}

// writeSearchConfig writes a one-task TOML whose run command prints the
// supplied marker. The task is API-triggered only.
func writeSearchConfig(t *testing.T, taskName, marker string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "runwisp.search.toml")
	body := fmt.Sprintf(`
[daemon]
shutdown_timeout = "500ms"

[tasks.%s]
run = "printf '%%s\n' 'preamble' '%s' 'trailing line'"
`, taskName, marker)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// waitForRunCompleted drains SSE until a run.completed (or run.failed) for the
// given id arrives. Other events are ignored.
func waitForRunCompleted(t *testing.T, stream <-chan apiclient.RunStreamEvent, runID string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-stream:
			if !ok {
				t.Fatal("SSE stream closed before run completion")
			}
			if ev.Type != "run.completed" && ev.Type != "run.failed" {
				continue
			}
			// The run.completed payload mirrors run.created — we only need to
			// confirm the id; parsing is delegated to the existing helper
			// pattern (a substring match keeps this from depending on the
			// envelope shape).
			if containsString(ev.Data, runID) {
				return
			}
		case <-deadline:
			t.Fatalf("run %s never reached a terminal state within %s", runID, timeout)
		}
	}
}

func containsString(haystack []byte, needle string) bool {
	if needle == "" {
		return true
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}
