//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
	"github.com/runwisp/runwisp/apps/runwisp/internal/testutil"
	"github.com/stretchr/testify/require"
)

// TestRestartAfterKill9StopsOrphanedRun: a run leads its own process group, so
// a kill -9 of a daemon started from a shell leaves a quiet job running. The
// next daemon must stop it through the task's stop ladder before marking the
// run crashed, or the next firing runs a second copy next to it, and say so in
// the run's own log.
func TestRestartAfterKill9StopsOrphanedRun(t *testing.T) {
	t.Parallel()
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	dir := testutil.ShortTempDir(t)
	pidFile := filepath.Join(dir, "job.pid")
	termFile := filepath.Join(dir, "job.term")
	configPath := filepath.Join(dir, "runwisp.toml")
	config := fmt.Sprintf(`
[tasks.quiet]
graceful_stop = "30s"
# The old daemon took the read end of the job's stdout/stderr with it, so the
# shell's own "Terminated" notice would SIGPIPE it before its trap runs.
run = '''exec >/dev/null 2>&1; trap 'touch %s; exit 0' TERM; echo $$ > %s; while :; do sleep 0.1; done'''
`, termFile, pidFile)
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0o600))

	daemon := startDaemon(t, projectDir, binaryPath, configPath)
	run, err := socketClient(t, daemon.dataDir).TriggerRun(t.Context(), "quiet", nil, "cli")
	require.NoError(t, err)

	var pid int
	require.Eventually(t, func() bool {
		raw, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(raw)))
		return err == nil && pid > 0
	}, 10*time.Second, 50*time.Millisecond, "job should write its pid")
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })

	// The run's process group is persisted asynchronously just after spawn;
	// wait for it so the kill below can't beat the write.
	db, err := storage.New(filepath.Join(daemon.dataDir, "runwisp.db"))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		runs, err := db.GetRunningRuns(t.Context())
		return err == nil && len(runs) == 1 && runs[0].Pgid == pid
	}, 10*time.Second, 50*time.Millisecond, "the run should record its process group")
	require.NoError(t, db.Close())

	require.NoError(t, daemon.cmd.Process.Kill(), "kill -9 the daemon alone, not its group")
	require.True(t, daemon.waitForExit(processExitTimeout))
	require.NoError(t, syscall.Kill(pid, 0), "the job outlives a kill -9 of its daemon")

	startDaemonOn(t, projectDir, binaryPath, configPath, daemon.dataDir, daemon.port)
	require.Eventually(t, func() bool { return syscall.Kill(pid, 0) != nil },
		5*time.Second, 50*time.Millisecond, "the restarted daemon should stop the orphaned job")
	require.FileExists(t, termFile, "the job gets its stop_signal before any SIGKILL")

	raw, err := socketClient(t, daemon.dataDir).GetLogRaw(t.Context(), run.ID)
	require.NoError(t, err)
	defer raw.Close()
	body, err := io.ReadAll(raw)
	require.NoError(t, err)
	require.Contains(t, string(body), "found this run still going", "the crashed run's log should say its processes were stopped")
}
