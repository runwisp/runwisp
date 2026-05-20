//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEnvMergedIntoSpawnedProcessAndAPIHidesSecrets verifies the end-to-end
// behavior of `env` / `env_file`: defaults are flattened into the task, the
// task overrides defaults on collision, and the daemon spawns a process whose
// env contains the merged set. It also asserts that the API exposes inline
// env + env_file path while keeping env_file values entirely off the wire.
func TestEnvMergedIntoSpawnedProcessAndAPIHidesSecrets(t *testing.T) {
	const (
		taskName    = "env-task"
		secretValue = "secret-value-do-not-leak"
	)

	dir := t.TempDir()
	envOut := filepath.Join(dir, "env.out")
	envFile := filepath.Join(dir, "secrets.env")
	require.NoError(t, os.WriteFile(envFile,
		[]byte("SECRET="+secretValue+"\n"), 0o600))

	configPath := filepath.Join(dir, "runwisp.toml")
	body := fmt.Sprintf(`
[daemon]
shutdown_timeout = "500ms"

[defaults.env]
GLOBAL = "from-defaults"

[tasks.%s]
run = "env | grep -E '^(GLOBAL|TASK|SECRET)=' | sort > %s"
env_file = "secrets.env"

[tasks.%s.env]
TASK = "from-task"
GLOBAL = "task-overrides-defaults"
`, taskName, envOut, taskName)
	require.NoError(t, os.WriteFile(configPath, []byte(body), 0o600))

	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	client := socketClient(t, daemon.dataDir)

	triggered, err := client.TriggerRun(taskName)
	require.NoError(t, err)
	require.NotEmpty(t, triggered.ID)

	// Poll until the script has written its env capture.
	deadline := time.Now().Add(10 * time.Second)
	var captured []byte
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(envOut); err == nil && len(data) > 0 {
			captured = data
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.NotEmpty(t, captured, "env capture file never materialized")

	assert.Equal(t,
		"GLOBAL=task-overrides-defaults\nSECRET="+secretValue+"\nTASK=from-task\n",
		string(captured),
		"spawned process must see the merged env with task overriding defaults",
	)

	// Verify the API exposes inline env + env_file path but never the env_file
	// content. Reach for the typed response *and* the raw JSON so a future
	// `json:"-"` regression on Task.SecretEnv is caught at the wire level.
	tasks, err := client.ListTasks()
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "from-task", tasks[0].Env["TASK"])
	assert.Equal(t, "task-overrides-defaults", tasks[0].Env["GLOBAL"])
	assert.Equal(t, "secrets.env", tasks[0].EnvFile)

	rawJSON, err := json.Marshal(tasks)
	require.NoError(t, err)
	body2 := string(rawJSON)
	assert.NotContains(t, body2, secretValue, "env_file values must never reach the API surface")
	assert.NotContains(t, body2, "SECRET", "env_file keys must never reach the API surface")
}
