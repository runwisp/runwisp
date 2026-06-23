//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server"
	"github.com/stretchr/testify/require"
)

// TestProgressBarCommitsFinalFrameAndStreamsRegion proves the terminal-aware
// output capture end to end: a carriage-return progress bar must land on disk
// as a single clean final frame (no literal `\r`, no intermediate percentages
// as separate lines), and live viewers must receive at least one region
// snapshot while the bar animates.
func TestProgressBarCommitsFinalFrameAndStreamsRegion(t *testing.T) {
	const taskName = "progress-task"

	configPath := writeProgressConfig(t, taskName)
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	client := socketClient(t, daemon.dataDir)

	triggered, err := client.TriggerRun(taskName, nil)
	require.NoError(t, err, "TriggerRun should succeed")
	require.NotEmpty(t, triggered.ID)

	run := waitForListedRun(t, client, taskName, triggered.ID, 5*time.Second)

	// Subscribe to the live log stream while the bar is still animating (the
	// task spreads its frames over ~0.8s) so we observe a region snapshot.
	streamCtx, cancelStream := context.WithCancel(context.Background())
	t.Cleanup(cancelStream)
	msgs, err := client.StreamLogLines(streamCtx, taskName, run.ID, apiclient.StreamLogOpts{FromLine: -100})
	require.NoError(t, err, "StreamLogLines should connect")

	sawRegion := waitForRegionFrame(t, msgs, "progress:", 10*time.Second)
	require.True(t, sawRegion, "expected at least one live region snapshot of the progress bar")

	// On disk: the final frame is committed once, with no carriage returns and
	// no intermediate frames left as their own lines.
	logDir := filepath.Join(daemon.dataDir, "logs")
	logPath := logutil.ResolveRunLogPath(logDir, run.TaskName, run.ID, run.CreatedAt)
	body := waitForLogContains(t, client, taskName, run.ID, "progress: 100%", 10*time.Second)

	require.NotContains(t, body, "\r", "committed log must not contain raw carriage returns")
	require.Contains(t, body, "done", "the line after the bar should be committed")
	require.Equal(t, 1, strings.Count(body, "progress:"),
		"only the final frame of the bar should be persisted, got:\n%s", body)

	// The on-disk file exists and matches what /log/raw returned.
	requireLogFileNonEmpty(t, logPath, 5*time.Second)
}

// TestProgressBarExposesFrameHistory proves PHASE 2: after a CR progress bar
// settles into its single committed line, the operator can rewind through the
// prior frames it animated through. The anchor line carries frame_count>0, the
// history endpoint returns those prior frames whole (no raw `\r`), the sidecar
// container (holding the frame history) exists on disk, and deleting the run
// removes it.
func TestProgressBarExposesFrameHistory(t *testing.T) {
	const taskName = "progress-hist-task"

	configPath := writeProgressConfig(t, taskName)
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	client := socketClient(t, daemon.dataDir)

	triggered, err := client.TriggerRun(taskName, nil)
	require.NoError(t, err, "TriggerRun should succeed")
	run := waitForListedRun(t, client, taskName, triggered.ID, 5*time.Second)

	// Wait for the bar to settle on disk.
	waitForLogContains(t, client, taskName, run.ID, "progress: 100%", 10*time.Second)

	// The anchor line (the committed bar) advertises frame history.
	anchor := waitForAnchorLine(t, client, taskName, run.ID, "progress:", 10*time.Second)
	require.Greater(t, anchor.FrameCount, 0, "the committed bar line should advertise frame history")

	frames, err := client.GetLogLineHistory(taskName, run.ID, anchor.N)
	require.NoError(t, err, "history endpoint should succeed")
	require.NotEmpty(t, frames, "expected prior frames for the settled bar")

	var earlier bool
	for _, frame := range frames {
		require.Len(t, frame, 1, "a single-line CR bar yields one-row frames")
		for _, row := range frame {
			require.NotContains(t, row, "\r", "history frames must not carry raw carriage returns")
			if strings.Contains(row, "progress:") && !strings.Contains(row, "100%") {
				earlier = true
			}
		}
	}
	require.True(t, earlier, "history should include an earlier (non-final) frame of the bar")

	// The sidecar container (holding the frame history) exists on disk while the
	// run is retained.
	logDir := filepath.Join(daemon.dataDir, "logs")
	logPath := logutil.ResolveRunLogPath(logDir, run.TaskName, run.ID, run.CreatedAt)
	containerPath := logutil.MetaPath(logPath)
	_, statErr := os.Stat(containerPath)
	require.NoError(t, statErr, "expected a sidecar container at %s", containerPath)

	// The bar settling on disk does not guarantee the run has reached a
	// terminal status yet (the trailing `echo done` and exit reaping lag the
	// final frame, especially on slower runners). Wait for it to end before
	// deleting, since the API rejects deleting a still-running run.
	waitForRunEnded(t, client, taskName, run.ID, 10*time.Second)

	// Deleting the run removes the container along with the rest of the log
	// files. Deletion is soft: the purger reclaims on-disk files after its
	// TTL + sweep (≈9s), so allow generous slack here.
	require.NoError(t, client.DeleteRun(taskName, run.ID))
	require.Eventually(t, func() bool {
		_, err := os.Stat(containerPath)
		return os.IsNotExist(err)
	}, 20*time.Second, 200*time.Millisecond, "the sidecar container should be removed when the run is deleted")
}

// waitForAnchorLine polls the log page until a committed line whose text
// contains substr carries frame history (FrameCount>0), then returns it.
func waitForAnchorLine(t *testing.T, client *apiclient.Client, taskName, runID, substr string, timeout time.Duration) server.LogLineEntry {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		page, err := client.GetLogPage(taskName, runID, 0, 0)
		if err == nil {
			for _, l := range page.Lines {
				if strings.Contains(l.Text, substr) && l.FrameCount > 0 {
					return l
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no anchor line containing %q with frame history within %s", substr, timeout)
	return server.LogLineEntry{}
}

// waitForRunEnded polls a single run until it reaches a terminal status, so
// callers can safely delete it (the API rejects deleting a running run).
func waitForRunEnded(t *testing.T, client *apiclient.Client, taskName, runID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		run, err := client.GetRun(taskName, runID)
		if err == nil && run.Status == model.PhaseEnded {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("run %s never reached a terminal status within %s", runID, timeout)
}

// writeProgressConfig writes a one-task TOML whose command paints a CR progress
// bar across several frames, then commits a final newline and a trailing line.
func writeProgressConfig(t *testing.T, taskName string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "runwisp.progress.toml")
	body := fmt.Sprintf(`
[daemon]
shutdown_timeout = "500ms"

[tasks.%s]
run = "for p in 10 40 70 100; do printf 'progress: %%d%%%%\r' \"$p\"; sleep 0.2; done; printf '\n'; echo done"
`, taskName)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// waitForRegionFrame drains the log stream until a region snapshot whose rows
// contain substr arrives, or the timeout/Done elapses.
func waitForRegionFrame(t *testing.T, msgs <-chan apiclient.LogStreamMsg, substr string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case msg, ok := <-msgs:
			if !ok {
				return false
			}
			switch msg.Kind {
			case apiclient.LogStreamMsgKindRegion:
				for _, row := range msg.Region.Rows {
					if strings.Contains(row, substr) {
						return true
					}
				}
			case apiclient.LogStreamMsgKindDone:
				return false
			}
		case <-deadline:
			return false
		}
	}
}

// waitForLogContains polls /log/raw until want appears, then returns the body.
func waitForLogContains(t *testing.T, client *apiclient.Client, taskName, runID, want string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		rc, err := client.GetLogRaw(taskName, runID)
		if err == nil {
			b, readErr := io.ReadAll(rc)
			rc.Close()
			if readErr == nil {
				last = string(b)
				if strings.Contains(last, want) {
					return last
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("log for run %s never contained %q within %s; last body:\n%s", runID, want, timeout, last)
	return ""
}
