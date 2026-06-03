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
// behavior of the env/secrets split: defaults are flattened into the task,
// inline env wins over env_file, and the daemon spawns a process whose env
// contains all five layers merged (env_file < env < secrets_file < secrets).
// On the API side, env + env_file values are visible while secrets keys and
// values never reach the wire — only the secrets_file path is exposed.
func TestEnvMergedIntoSpawnedProcessAndAPIHidesSecrets(t *testing.T) {
	const (
		taskName          = "env-task"
		fileSecretValue   = "file-secret-do-not-leak"
		inlineSecretValue = "inline-secret-do-not-leak"
	)

	dir := t.TempDir()
	envOut := filepath.Join(dir, "env.out")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "vars.env"),
		[]byte("PUBLIC=from-env-file\nTASK=env-file-loses\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "secrets.env"),
		[]byte("FILE_SECRET="+fileSecretValue+"\n"), 0o600))

	configPath := filepath.Join(dir, "runwisp.toml")
	body := fmt.Sprintf(`
[daemon]
shutdown_timeout = "500ms"

[defaults.env]
GLOBAL = "from-defaults"

[tasks.%s]
run = "env | grep -E '^(GLOBAL|TASK|PUBLIC|FILE_SECRET|INLINE_SECRET)=' | sort > %s"
env_file = "vars.env"
secrets_file = "secrets.env"

[tasks.%s.env]
TASK = "from-task"
GLOBAL = "task-overrides-defaults"

[tasks.%s.secrets]
INLINE_SECRET = "%s"
`, taskName, envOut, taskName, taskName, inlineSecretValue)
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
		"FILE_SECRET="+fileSecretValue+"\n"+
			"GLOBAL=task-overrides-defaults\n"+
			"INLINE_SECRET="+inlineSecretValue+"\n"+
			"PUBLIC=from-env-file\n"+
			"TASK=from-task\n",
		string(captured),
		"spawned process must see env_file, inline env, and both secrets layers merged, with inline env winning over env_file",
	)

	// Verify the API exposes env (including merged env_file values) and both
	// file paths, but never secrets keys or values. Reach for the typed
	// response *and* the raw JSON so a future `json:"-"` regression on
	// Task.Secrets is caught at the wire level.
	tasks, err := client.ListTasks()
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "from-task", tasks[0].Env["TASK"], "inline env wins over env_file")
	assert.Equal(t, "task-overrides-defaults", tasks[0].Env["GLOBAL"])
	assert.Equal(t, "from-env-file", tasks[0].Env["PUBLIC"], "env_file values are visible in the API")
	assert.Equal(t, "vars.env", tasks[0].EnvFile)
	assert.Equal(t, "secrets.env", tasks[0].SecretsFile, "secrets_file path is visible — only keys/values are hidden")

	rawJSON, err := json.Marshal(tasks)
	require.NoError(t, err)
	body2 := string(rawJSON)
	assert.Contains(t, body2, "from-env-file", "env_file values are part of the visible env")
	assert.NotContains(t, body2, fileSecretValue, "secrets_file values must never reach the API surface")
	assert.NotContains(t, body2, inlineSecretValue, "inline secrets values must never reach the API surface")
	assert.NotContains(t, body2, "FILE_SECRET", "secrets keys must never reach the API surface")
	assert.NotContains(t, body2, "INLINE_SECRET", "secrets keys must never reach the API surface")
}
