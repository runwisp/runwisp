//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIncludeCron_EndToEnd is the whole crond-replacement story against the real
// binary: point the daemon at a crontab, and its jobs run — with run records,
// which is the claim that matters. A test that only checked the task appeared
// would pass on a config that loads and schedules nothing.
//
// It also pins the two things that make include_cron safe to leave running: a job
// RunWisp can't reproduce is reported rather than silently absent, and an edit
// plus `runwisp reload` picks the file up.
func TestIncludeCron_EndToEnd(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	configDir := t.TempDir()
	cronDir := filepath.Join(configDir, "crontabs")
	require.NoError(t, os.MkdirAll(cronDir, 0o755))

	configPath := filepath.Join(configDir, "runwisp.toml")
	require.NoError(t, os.WriteFile(configPath, []byte(`
[daemon]
shutdown_timeout = "500ms"
include_cron = ["crontabs/*"]
`), 0o600))

	// One job that runs every minute so it produces a record on its own, and one
	// whose schedule can't be parsed at all.
	cronPath := filepath.Join(cronDir, "jobs")
	require.NoError(t, os.WriteFile(cronPath, []byte(
		"* * * * * echo ticked\n"+
			"99 99 * * * /usr/local/bin/bad.sh\n"), 0o600))

	daemon := startDaemon(t, projectDir, binaryPath, configPath)
	client := socketClient(t, daemon.dataDir)

	require.Equal(t, []string{"echo"}, taskNames(t, client),
		"the good job is live and the unparseable one is not")

	// PD#1: the task exists *and* runs. Trigger it rather than waiting out a
	// minute of wall clock — the schedule is proven by the task set above.
	run, err := client.TriggerRun("echo", nil)
	require.NoError(t, err)
	require.NotEmpty(t, run.ID)
	waitForRunCount(t, client, "echo", 1, 15*time.Second)

	// The skipped job is named where the operator looks, by file and line.
	status, err := runCLI(t, projectDir, binaryPath,
		"status", "--data", daemon.dataDir, "--config", configPath)
	require.NoError(t, err, "status should succeed: %s", status)

	validate, err := runCLI(t, projectDir, binaryPath, "validate", "--config", configPath)
	require.NoError(t, err, "a crontab with one bad job still validates: %s", validate)
	assert.Contains(t, validate, "jobs:2")
	assert.Contains(t, validate, "not running")

	// `crontab -e` then `runwisp reload`: the new job goes live, and the reload
	// output still names the job that isn't running — the reload is the moment the
	// operator is watching.
	require.NoError(t, os.WriteFile(cronPath, []byte(
		"* * * * * echo ticked\n"+
			"99 99 * * * /usr/local/bin/bad.sh\n"+
			"0 3 * * * /usr/local/bin/nightly.sh\n"), 0o600))

	out, err := runCLI(t, projectDir, binaryPath,
		"reload", "--data", daemon.dataDir, "--config", configPath)
	require.NoError(t, err, "reload should succeed: %s", out)
	assert.Contains(t, out, "nightly")
	assert.Contains(t, out, "jobs:2", "the reload still reports the skipped job")

	assert.Equal(t, []string{"echo", "nightly"}, taskNames(t, client))
}
