//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestValidateRejectsNeverFiringCron guards the cron grammar at the CLI
// boundary: go-cron's never-firing descriptors and wrap-around ranges that
// start outside the field's bounds used to pass `validate` and then misbehave
// at runtime (a zero Next spun catch-up forever at boot). A wrapped range that
// is inside the bounds must still validate.
func TestValidateRejectsNeverFiringCron(t *testing.T) {
	t.Parallel()
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	tests := []struct {
		cron    string
		invalid bool
	}{
		{cron: "@manual", invalid: true},
		{cron: "@triggered", invalid: true},
		{cron: "@none", invalid: true},
		{cron: "0 30-2 * * *", invalid: true},
		{cron: "0 22-2 * * *"},
		{cron: "0 3 * * FRI-MON/2"},
	}
	for _, tt := range tests {
		t.Run(tt.cron, func(t *testing.T) {
			t.Parallel()
			configPath := filepath.Join(t.TempDir(), "runwisp.toml")
			require.NoError(t, os.WriteFile(configPath, []byte(
				"[tasks.t]\ncron = "+strconv.Quote(tt.cron)+"\nrun = \"true\"\n"), 0o600))

			out, err := runCLI(t, projectDir, binaryPath, "validate", "--config", configPath)
			if !tt.invalid {
				require.NoError(t, err, "validate should accept %q: %s", tt.cron, out)
				return
			}
			require.Error(t, err, "validate should reject %q: %s", tt.cron, out)
			assert.Contains(t, out, "invalid cron for task")
		})
	}
}

// TestDaemonBootWithCronCatchUp boots a daemon over a long downtime gap on a
// wrap-around cron. Catch-up must count the missed ticks and finish; the
// readiness wait in startDaemonOn is what a hung catch-up would trip.
func TestDaemonBootWithCronCatchUp(t *testing.T) {
	t.Parallel()
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	configPath := filepath.Join(t.TempDir(), "runwisp.toml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
[daemon]
shutdown_timeout = "500ms"

[tasks.nightly]
cron = "0 22-2 * * *"
catch_up = 0
run = "true"
`), 0o600))

	dataDir := testutil.ShortTempDir(t)
	seedAnchorRun(t, dataDir, "nightly", -72*time.Hour)

	daemon := startDaemonOn(t, projectDir, binaryPath, configPath, dataDir, reserveTCPPort(t))
	client := socketClient(t, daemon.dataDir)

	require.Eventually(t, func() bool {
		s, err := client.GetRunSummary(t.Context())
		return err == nil && s.Missed == 1
	}, 10*time.Second, 100*time.Millisecond, "catch-up must finish and record the missed gap")
}

// TestDaemonRefusesNeverFiringCron proves the daemon itself, not just
// `validate`, exits with an error on a cron that never fires instead of
// booting and hanging in catch-up over a seeded downtime gap.
func TestDaemonRefusesNeverFiringCron(t *testing.T) {
	t.Parallel()
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	configPath := filepath.Join(t.TempDir(), "runwisp.toml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
[tasks.never]
cron = "@manual"
run = "true"
`), 0o600))

	dataDir := testutil.ShortTempDir(t)
	seedAnchorRun(t, dataDir, "never", -72*time.Hour)

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binaryPath,
		"--config", configPath, "--data", dataDir,
		"--port", strconv.Itoa(reserveTCPPort(t)), "daemon")
	cmd.Dir = projectDir
	cmd.Env = subprocEnv()
	out, err := cmd.CombinedOutput()

	require.NoError(t, ctx.Err(), "daemon must exit on its own, not hang: %s", out)
	require.Error(t, err, "daemon must refuse the config: %s", out)
	assert.Contains(t, string(out), "never fires on its own")
}
