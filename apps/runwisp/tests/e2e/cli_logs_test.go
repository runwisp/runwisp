//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"encoding/json"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCLILogs drives `runwisp logs` against a real daemon: a snapshot of a
// finished run (both streams, and its outcome), the --json records, following
// a run ID that already ended, and a live follow that picks up a run started
// after it attached.
func TestCLILogs(t *testing.T) {
	t.Parallel()
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	configPath := writeE2EConfig(t, t.TempDir())
	daemon := startDaemon(t, projectDir, binaryPath, configPath)
	base := []string{"--data", daemon.dataDir, "--config", configPath}
	cli := func(args ...string) *exec.Cmd {
		cmd := exec.Command(binaryPath, append(args, base...)...)
		cmd.Dir = projectDir
		cmd.Env = subprocEnv()
		return cmd
	}

	out, err := cli("run", "bravo-fail", "--daemon").CombinedOutput()
	require.Error(t, err, "bravo-fail exits 1: %s", out)

	out, err = cli("logs", "bravo-fail").CombinedOutput()
	require.NoError(t, err, "%s", out)
	assert.Contains(t, string(out), "bravo-line-1")
	assert.Contains(t, string(out), "bravo-line-2", "stderr lines are shown too")
	assert.Contains(t, string(out), "run failed", "the outcome is reported")

	jsonOut, err := cli("logs", "--json", "bravo-fail").Output()
	require.NoError(t, err)
	recs := strings.Split(strings.TrimSpace(string(jsonOut)), "\n")
	require.Len(t, recs, 3, "two lines and the outcome: %s", jsonOut)
	var end struct {
		Type     string `json:"type"`
		RunID    string `json:"runId"`
		ExitCode int    `json:"exitCode"`
		Failed   bool   `json:"failed"`
	}
	require.NoError(t, json.Unmarshal([]byte(recs[2]), &end))
	assert.Equal(t, "end", end.Type)
	assert.Equal(t, 1, end.ExitCode)
	assert.True(t, end.Failed)

	out, err = cli("logs", "-f", end.RunID).CombinedOutput()
	require.NoError(t, err, "following an ended run ID replays it and exits: %s", out)
	assert.Contains(t, string(out), "bravo-line-2")

	// A live follow attaches before alpha-stream has any run, then picks the
	// new run up from its first line.
	follow := cli("logs", "-f", "alpha-*")
	output := &lockedBuffer{}
	follow.Stdout, follow.Stderr = output, output
	require.NoError(t, follow.Start())
	t.Cleanup(func() { _ = follow.Process.Kill() })
	require.Eventually(t, func() bool { return strings.Contains(output.Tail(4096), "No runs yet") },
		15*time.Second, 50*time.Millisecond, "follow never attached: %s", output.Tail(4096))

	out, err = cli("start", "alpha-stream").CombinedOutput()
	require.NoError(t, err, "%s", out)
	require.Eventually(t, func() bool {
		got := output.Tail(4096)
		return strings.Contains(got, "alpha-line-1") && strings.Contains(got, "alpha-line-3") && strings.Contains(got, "run succeeded")
	}, 20*time.Second, 50*time.Millisecond, "follow output: %s", output.Tail(4096))

	require.NoError(t, follow.Process.Signal(syscall.SIGTERM))
	require.NoError(t, follow.Wait(), "SIGTERM ends a follow cleanly")
}
