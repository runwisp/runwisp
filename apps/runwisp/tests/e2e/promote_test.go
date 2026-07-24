//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCLIImportThenPromote walks the whole v1 steering loop against the real
// binary: import a crontab into the two-tier layout, promote one job into the
// operator's own config, then promote the rest and watch the staging file retire.
// Each step is checked the way an operator would check it — `validate` and
// `list --json`.
func TestCLIImportThenPromote(t *testing.T) {
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	workDir := t.TempDir()
	crontab := filepath.Join(workDir, "crontab.txt")
	require.NoError(t, os.WriteFile(crontab, []byte(
		"0 3 * * * /usr/bin/backup.sh --full\n"+
			"*/5 * * * * reindex --all\n"), 0o600))

	out, err := runCLI(t, workDir, binaryPath, "import", "cron", crontab, "--write")
	require.NoError(t, err, "import should succeed: %s", out)

	rootPath := filepath.Join(workDir, "runwisp.toml")
	stagingPath := filepath.Join(workDir, "runwisp.d", "imported.toml")
	require.FileExists(t, rootPath)
	require.FileExists(t, stagingPath)

	// Both jobs start staged.
	assert.Equal(t, map[string]bool{"backup": true, "reindex": true}, listedStaged(t, workDir, binaryPath))

	// Promote one: its block leaves staging for the root, and the comment the
	// importer attached to it comes along.
	out, err = runCLI(t, workDir, binaryPath, "promote", "backup")
	require.NoError(t, err, "promote should succeed: %s", out)
	assert.Contains(t, out, "Promoted 1 task")

	root := readE2EFile(t, rootPath)
	assert.Contains(t, root, "[tasks.backup]")
	assert.Contains(t, root, "/usr/bin/backup.sh --full")
	assert.NotContains(t, readE2EFile(t, stagingPath), "[tasks.backup]")

	out, err = runCLI(t, workDir, binaryPath, "validate")
	require.NoError(t, err, "the promoted config must validate: %s", out)

	assert.Equal(t, map[string]bool{"backup": false, "reindex": true}, listedStaged(t, workDir, binaryPath))

	// Promote the rest: the staging file has nothing left to say, so it goes.
	out, err = runCLI(t, workDir, binaryPath, "promote", "--all")
	require.NoError(t, err, "promote --all should succeed: %s", out)
	assert.NoFileExists(t, stagingPath)

	out, err = runCLI(t, workDir, binaryPath, "validate")
	require.NoError(t, err, "the config must still validate with staging gone: %s", out)
	assert.Equal(t, map[string]bool{"backup": false, "reindex": false}, listedStaged(t, workDir, binaryPath))

	// And re-running is a no-op rather than a failure, so provisioning scripts can
	// call it unconditionally.
	out, err = runCLI(t, workDir, binaryPath, "promote", "--all")
	require.NoError(t, err, "a second promote --all should exit 0: %s", out)
	assert.Contains(t, out, "Nothing to promote")
}

// listedStaged reports each configured task's staged flag via `list --json`, the
// surface the UI and any agent reads.
func listedStaged(t *testing.T, workDir, binaryPath string) map[string]bool {
	t.Helper()

	out, err := runCLI(t, workDir, binaryPath, "list", "--json")
	require.NoError(t, err, "list --json should succeed: %s", out)

	var doc struct {
		Tasks []struct {
			Name   string `json:"name"`
			Staged bool   `json:"staged"`
		} `json:"tasks"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &doc), "list --json output: %s", out)

	staged := make(map[string]bool, len(doc.Tasks))
	for _, task := range doc.Tasks {
		staged[task.Name] = task.Staged
	}
	return staged
}

// readE2EFile returns a file's contents, failing the test if it can't be read.
func readE2EFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}
