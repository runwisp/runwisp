//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResourceUsageLiveAndPersisted runs a shell task and checks both halves of
// resource usage: the task reports live usage while it runs, and the finished
// run carries its peak memory and CPU time.
func TestResourceUsageLiveAndPersisted(t *testing.T) {
	t.Parallel()
	const taskName = "usage-task"

	configPath := filepath.Join(t.TempDir(), "runwisp.toml")
	body := fmt.Sprintf(`
[daemon]
shutdown_timeout = "500ms"

[tasks.%s]
run = "sleep 3"
`, taskName)
	require.NoError(t, os.WriteFile(configPath, []byte(body), 0o600))

	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)
	client := socketClient(t, daemon.dataDir)

	triggered, err := client.TriggerRun(t.Context(), taskName, nil, "")
	require.NoError(t, err)

	var live *model.ResourceUsage
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && live == nil {
		tasks, err := client.ListTasks(t.Context())
		require.NoError(t, err)
		for _, task := range tasks {
			if task.Name == taskName {
				live = task.Usage
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	require.NotNil(t, live, "a running shell task reports live usage")
	assert.Positive(t, live.MemoryBytes)

	waitForRunEnded(t, client, triggered.ID, 10*time.Second)
	run, err := client.GetRun(t.Context(), triggered.ID)
	require.NoError(t, err)
	require.NotNil(t, run.PeakMemoryBytes, "the finished run records its peak memory")
	assert.Positive(t, *run.PeakMemoryBytes)
	require.NotNil(t, run.CPUTimeMs, "the finished run records its CPU time")
}
