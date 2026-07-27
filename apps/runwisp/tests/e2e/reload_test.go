//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"os"
	"sort"
	"syscall"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reloadBaseConfig is the task set the daemon boots with: two cron tasks under
// an explicit UTC scheduler so changing [scheduler] timezone is a meaningful
// non-reloadable edit.
const reloadBaseConfig = `
[daemon]
shutdown_timeout = "500ms"

[scheduler]
timezone = "UTC"

[tasks.keep]
cron = "0 0 * * *"
run = "echo keep"

[tasks.drop]
cron = "0 0 * * *"
run = "echo drop"
`

// reloadEditedConfig keeps "keep" (with a changed cron), removes "drop", and
// adds "fresh".
const reloadEditedConfig = `
[daemon]
shutdown_timeout = "500ms"

[scheduler]
timezone = "UTC"

[tasks.keep]
cron = "0 12 * * *"
run = "echo keep"

[tasks.fresh]
cron = "0 0 * * *"
run = "echo fresh"
`

func writeReloadConfig(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
}

func taskNames(t *testing.T, client *apiclient.Client) []string {
	t.Helper()
	tasks, err := client.ListTasks()
	require.NoError(t, err)
	names := make([]string, 0, len(tasks))
	for _, task := range tasks {
		names = append(names, task.Name)
	}
	sort.Strings(names)
	return names
}

// waitForTaskNames polls the live task set until it matches want, so SIGHUP
// reloads (which return no synchronous result to the test) can be observed.
func waitForTaskNames(t *testing.T, client *apiclient.Client, want []string) {
	t.Helper()
	require.Eventually(t, func() bool {
		got := taskNames(t, client)
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}, 5*time.Second, 100*time.Millisecond, "live task set should converge to %v", want)
}

// TestReloadViaCLI exercises the full happy path through `runwisp reload`: an
// add, a change, and a remove are all applied live and reported in the diff.
func TestReloadViaCLI(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	configDir := t.TempDir()
	configPath := configDir + "/runwisp.toml"
	writeReloadConfig(t, configPath, reloadBaseConfig)

	daemon := startDaemon(t, projectDir, binaryPath, configPath)
	client := socketClient(t, daemon.dataDir)

	require.Equal(t, []string{"drop", "keep"}, taskNames(t, client))

	writeReloadConfig(t, configPath, reloadEditedConfig)

	out, err := runCLI(t, projectDir, binaryPath,
		"reload", "--data", daemon.dataDir, "--config", configPath)
	require.NoError(t, err, "reload should succeed: %s", out)
	assert.Contains(t, out, "added")
	assert.Contains(t, out, "fresh")
	assert.Contains(t, out, "changed")
	assert.Contains(t, out, "keep")
	assert.Contains(t, out, "removed")
	assert.Contains(t, out, "drop")

	assert.Equal(t, []string{"fresh", "keep"}, taskNames(t, client),
		"reloaded task set must reflect the edited config")
}

// TestReloadViaSIGHUP proves SIGHUP drives the same reconcile as the CLI and
// never tears the daemon down.
func TestReloadViaSIGHUP(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	configDir := t.TempDir()
	configPath := configDir + "/runwisp.toml"
	writeReloadConfig(t, configPath, reloadBaseConfig)

	daemon := startDaemon(t, projectDir, binaryPath, configPath)
	client := socketClient(t, daemon.dataDir)
	require.Equal(t, []string{"drop", "keep"}, taskNames(t, client))

	writeReloadConfig(t, configPath, reloadEditedConfig)
	require.NoError(t, killProcessGroup(daemon.cmd.Process.Pid, syscall.SIGHUP))

	waitForTaskNames(t, client, []string{"fresh", "keep"})

	// SIGHUP is a reload, not a shutdown: the daemon must still be serving.
	assert.NoError(t, client.HealthCheck(), "SIGHUP must not stop the daemon")
}

// TestReloadRejectsInvalidConfig confirms validate-first atomicity: a config
// that fails to parse is rejected and the live task set is left intact.
func TestReloadRejectsInvalidConfig(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	configDir := t.TempDir()
	configPath := configDir + "/runwisp.toml"
	writeReloadConfig(t, configPath, reloadBaseConfig)

	daemon := startDaemon(t, projectDir, binaryPath, configPath)
	client := socketClient(t, daemon.dataDir)

	writeReloadConfig(t, configPath, "[tasks.bad\nthis is not valid toml")

	out, err := runCLI(t, projectDir, binaryPath,
		"reload", "--data", daemon.dataDir, "--config", configPath)
	require.Error(t, err, "reload of invalid config must fail: %s", out)

	assert.Equal(t, []string{"drop", "keep"}, taskNames(t, client),
		"a rejected reload must leave the live task set untouched")
}

// TestReloadRejectsNonReloadableKey confirms a change to a restart-only setting
// ([scheduler] timezone here) is rejected with guidance, and nothing changes.
func TestReloadRejectsNonReloadableKey(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	configDir := t.TempDir()
	configPath := configDir + "/runwisp.toml"
	writeReloadConfig(t, configPath, reloadBaseConfig)

	daemon := startDaemon(t, projectDir, binaryPath, configPath)
	client := socketClient(t, daemon.dataDir)

	// Same tasks, but flip the scheduler timezone — a non-reloadable change.
	tzChanged := `
[daemon]
shutdown_timeout = "500ms"

[scheduler]
timezone = "America/New_York"

[tasks.keep]
cron = "0 0 * * *"
run = "echo keep"

[tasks.drop]
cron = "0 0 * * *"
run = "echo drop"
`
	writeReloadConfig(t, configPath, tzChanged)

	out, err := runCLI(t, projectDir, binaryPath,
		"reload", "--data", daemon.dataDir, "--config", configPath)
	require.Error(t, err, "reload of a non-reloadable key must fail: %s", out)
	assert.Contains(t, out, "runwisp restart",
		"the rejection should point the operator at a full restart")

	assert.Equal(t, []string{"drop", "keep"}, taskNames(t, client),
		"a rejected reload must leave the live task set untouched")
}
