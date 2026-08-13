//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/require"
)

// writeQuickConfig writes a config with a single instant task for standalone exec tests.
func writeQuickConfig(t *testing.T, dir string) string {
	t.Helper()

	path := filepath.Join(dir, "quick.toml")
	require.NoError(t, os.WriteFile(path, []byte(`
[tasks.hello]
run = "echo 'hello world'"
`), 0o600))
	return path
}

func runCLI(t *testing.T, projectDir, binaryPath string, args ...string) (string, error) {
	t.Helper()

	cmd := exec.Command(binaryPath, args...)
	cmd.Dir = projectDir
	cmd.Env = subprocEnv()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestCLIValidateCmd(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	configDir := t.TempDir()
	configPath := writeE2EConfig(t, configDir)

	// Valid config should succeed and print summary.
	out, err := runCLI(t, projectDir, binaryPath, "validate", "--config", configPath)
	require.NoError(t, err, "validate should succeed: %s", out)
	require.Contains(t, out, "is valid")
	require.Contains(t, out, "tasks:")
	require.Contains(t, out, "services:")
	require.Contains(t, out, "timezone:")

	// Invalid config should fail.
	badPath := filepath.Join(configDir, "bad.toml")
	require.NoError(t, os.WriteFile(badPath, []byte("[tasks.bad\n"), 0o600))
	_, err = runCLI(t, projectDir, binaryPath, "validate", "--config", badPath)
	require.Error(t, err, "validate should fail on malformed TOML")
}

// TestCloudCmdBootErrorVisible guards against silent boot crashes in TUI mode.
// `runwisp cloud` (without --no-tui) reroutes slog into the TUI debug buffer
// before opening the config; that buffer only drains once the TUI attaches, so
// a boot failure used to exit 1 with no output at all. The fatal error must
// reach stderr. The cloud URL points at a closed port — boot fails at config
// parse, before any connection attempt, so the test stays offline.
func TestCloudCmdBootErrorVisible(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	configDir := t.TempDir()
	badPath := filepath.Join(configDir, "bad.toml")
	require.NoError(t, os.WriteFile(badPath, []byte("[tasks.bad\n"), 0o600))

	cmd := exec.Command(
		binaryPath,
		"--config", badPath,
		"--data", testutil.ShortTempDir(t),
		"cloud",
	)
	// configDir as cwd keeps the command away from any developer .env file
	// (the cloud subcommand loads ./.env by default).
	cmd.Dir = configDir
	cmd.Env = subprocEnv(
		"RUNWISP_CLOUD_TOKEN=rt_e2e_dummy_token",
		"RUNWISP_CLOUD_URL=https://127.0.0.1:1",
	)
	out, err := cmd.CombinedOutput()
	require.Error(t, err, "cloud mode with a malformed config must exit non-zero: %s", out)
	// Case-insensitive: the fatal error is rendered by fang's styled handler,
	// which capitalizes the first letter ("Failed to parse config file…"). What
	// matters is that the boot failure is visible, not its exact casing.
	require.Contains(t, strings.ToLower(string(out)), "failed to parse config file",
		"fatal boot error must be visible on stderr, got: %q", string(out))
}

func TestCLIListCmd(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	configDir := t.TempDir()
	configPath := writeE2EConfig(t, configDir)

	out, err := runCLI(t, projectDir, binaryPath, "list", "--config", configPath)
	require.NoError(t, err, "list should succeed: %s", out)
	require.Contains(t, out, "alpha-stream")
	require.Contains(t, out, "bravo-fail")
	require.Contains(t, out, "SCHEDULE")
}

func TestCLIStatusCmd(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	configDir := t.TempDir()
	configPath := writeE2EConfig(t, configDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	// status talks over the daemon's Unix socket, so it needs --data.
	out, err := runCLI(t, projectDir, binaryPath,
		"status",
		"--data", daemon.dataDir,
	)
	require.NoError(t, err, "status should succeed: %s", out)
	require.Contains(t, out, "healthy", "expected healthy output, got: %s", out)
	require.Contains(t, out, strconv.Itoa(daemon.port), "status should report the daemon's port")
	require.NotContains(t, out, "runwisp restart", "fresh config must not be reported stale")

	// Edit runwisp.toml while the daemon runs: reload is restart-only, so
	// status must call out that the on-disk config is no longer what's live.
	require.NoError(t, os.WriteFile(configPath, []byte(`
[tasks.edited-after-boot]
run = "echo changed"
`), 0o600))

	out, err = runCLI(t, projectDir, binaryPath,
		"status",
		"--data", daemon.dataDir,
	)
	require.NoError(t, err, "status should still succeed: %s", out)
	require.Contains(t, out, "changed since the daemon started")
	require.Contains(t, out, "runwisp restart")
}

func TestCLIOpenAPICmd(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	out, err := runCLI(t, projectDir, binaryPath, "openapi")
	require.NoError(t, err, "openapi should succeed: %s", out)
	require.Contains(t, out, `"openapi"`)
	require.Contains(t, out, `"paths"`)
}

func TestCLIExecViaDaemon(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	configDir := t.TempDir()
	configPath := writeE2EConfig(t, configDir)
	daemon := startDaemon(t, projectDir, binaryPath, configPath)

	// Retry only on a transport-level error: under heavy parallel CI load the
	// initial dial to the daemon's socket can blip. We do NOT retry on a
	// successful-but-incomplete run — require.Contains below stays a hard
	// assertion so a real log-streaming regression still fails the test.
	var out string
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		out, err = runCLI(t, projectDir, binaryPath,
			"run", "alpha-stream",
			"--data", daemon.dataDir,
			"--config", configPath,
			"--daemon",
		)
		if err == nil {
			break
		}
		t.Logf("exec via daemon attempt %d failed (retrying): %v\n%s", attempt, err, out)
		time.Sleep(200 * time.Millisecond)
	}
	require.NoError(t, err, "exec via daemon should succeed: %s", out)
	require.Contains(t, out, "alpha-line-1")
}

func TestCLIExecStandalone(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	configDir := t.TempDir()
	dataDir := t.TempDir()
	configPath := writeQuickConfig(t, configDir)

	out, err := runCLI(t, projectDir, binaryPath,
		"run", "hello",
		"--data", dataDir,
		"--config", configPath,
		"--standalone",
	)
	require.NoError(t, err, "exec --standalone should succeed: %s", out)
	require.Contains(t, out, "hello world")
}
