//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestInterpolationResolvesRuntimeHidesAndRedacts is the end-to-end proof of the
// ${VAR} interpolation feature:
//
//   - a typed field (timeout) resolves at load and is shown,
//   - inline env + run-script ${VAR} resolve at spawn so the process sees the
//     real value,
//   - the API shows the raw ${VAR} placeholder for an unrevealed var and the
//     resolved value for one listed in reveal_vars,
//   - the secret never appears anywhere on the API wire, and
//   - a run that echoes the secret has it [redacted] in the captured log file.
func TestInterpolationResolvesRuntimeHidesAndRedacts(t *testing.T) {
	const (
		taskName = "interp"
		secret   = "topsecret-value-9999"
		shown    = "shown-base-url"
	)
	t.Setenv("RW_E2E_SECRET", secret)
	t.Setenv("RW_E2E_SHOWN", shown)
	t.Setenv("RW_E2E_TIMEOUT", "45s")

	dir := t.TempDir()
	envOut := filepath.Join(dir, "env.out")
	configPath := filepath.Join(dir, "runwisp.toml")
	body := fmt.Sprintf(`
[daemon]
shutdown_timeout = "500ms"
reveal_vars      = ["RW_E2E_SHOWN"]

[tasks.%s]
api_trigger = true
timeout     = "${RW_E2E_TIMEOUT}"
run = '''
echo "secret is ${RW_E2E_SECRET}"
env | grep -E '^(APIKEY|BASEURL)=' | sort > %s
'''

[tasks.%s.env]
APIKEY  = "${RW_E2E_SECRET}"
BASEURL = "${RW_E2E_SHOWN}"
`, taskName, envOut, taskName)
	require.NoError(t, os.WriteFile(configPath, []byte(body), 0o600))

	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)
	client := socketClient(t, daemon.dataDir)

	triggered, err := client.TriggerRun(taskName)
	require.NoError(t, err)
	require.NotEmpty(t, triggered.ID)

	// 1. The spawned process received resolved values for both env vars.
	captured := waitForFile(t, envOut, 10*time.Second)
	assert.Equal(t,
		"APIKEY="+secret+"\nBASEURL="+shown+"\n",
		string(captured),
		"the process must see resolved ${VAR} env values",
	)

	// 2. The API reveals only the whitelisted var; the secret stays a placeholder
	//    and never appears on the wire. The typed timeout resolved at load.
	tasks, err := client.ListTasks()
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "${RW_E2E_SECRET}", tasks[0].Env["APIKEY"], "unrevealed var stays a placeholder")
	assert.Equal(t, shown, tasks[0].Env["BASEURL"], "reveal_vars var resolves in the API")
	assert.Equal(t, 45*time.Second, tasks[0].Timeout, "typed field resolved at load")

	rawJSON, err := json.Marshal(tasks)
	require.NoError(t, err)
	assert.NotContains(t, string(rawJSON), secret, "a hidden secret must never reach the API surface")

	// 3. The run echoed the secret; the captured log masks it on disk. Poll the
	//    log file until the echoed line has been flushed.
	run := waitForListedRun(t, client, taskName, triggered.ID, 5*time.Second)
	logPath := logutil.ResolveRunLogPath(filepath.Join(daemon.dataDir, "logs"), run.TaskName, run.ID, run.CreatedAt)
	deadline := time.Now().Add(10 * time.Second)
	var logBody string
	for time.Now().Before(deadline) {
		if data, readErr := os.ReadFile(logPath); readErr == nil && strings.Contains(string(data), "secret is ") {
			logBody = string(data)
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	require.NotEmpty(t, logBody, "run log line with the echoed secret never materialized")
	assert.Contains(t, logBody, "secret is [redacted]", "echoed secret is masked in the log file")
	assert.NotContains(t, logBody, secret, "raw secret must never land on disk")
}

// waitForFile polls until path is non-empty and returns its contents.
func waitForFile(t *testing.T, path string, timeout time.Duration) []byte {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
			return data
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("file %s never materialized within %s", path, timeout)
	return nil
}
