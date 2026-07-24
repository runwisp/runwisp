// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureStagingInclude_AppendsWhenNoDaemonTable(t *testing.T) {
	root := "[tasks.web]\nrun = \"echo hi\"\n"
	out, changed, err := EnsureStagingInclude([]byte(root), "/etc/runwisp")
	require.NoError(t, err)
	assert.True(t, changed)
	s := string(out)
	assert.Contains(t, s, "[tasks.web]")
	assert.Contains(t, s, "[daemon]")
	assert.Contains(t, s, `include = ["runwisp.d/*.toml"]`)
}

func TestEnsureStagingInclude_InsertsAfterExistingDaemonHeader(t *testing.T) {
	root := "[daemon]\nshutdown_timeout = \"10s\"\n\n[tasks.web]\nrun = \"echo hi\"\n"
	out, changed, err := EnsureStagingInclude([]byte(root), "/etc/runwisp")
	require.NoError(t, err)
	assert.True(t, changed)
	s := string(out)
	// The include line lands right after the [daemon] header, before the
	// existing key, and the rest of the file is untouched.
	daemonIdx := strings.Index(s, "[daemon]\n")
	includeIdx := strings.Index(s, `include = ["runwisp.d/*.toml"]`)
	shutdownIdx := strings.Index(s, "shutdown_timeout")
	require.Greater(t, includeIdx, daemonIdx)
	require.Greater(t, shutdownIdx, includeIdx)
	assert.Contains(t, s, "shutdown_timeout = \"10s\"")
}

func TestEnsureStagingInclude_NoChangeWhenAlreadyCovered(t *testing.T) {
	for _, pat := range []string{"runwisp.d/*.toml", "runwisp.d/imported.toml", "runwisp.d/*"} {
		root := "[daemon]\ninclude = [\"" + pat + "\"]\n"
		out, changed, err := EnsureStagingInclude([]byte(root), "/etc/runwisp")
		require.NoError(t, err, pat)
		assert.False(t, changed, "pattern %q should already cover the staging file", pat)
		assert.Equal(t, root, string(out), pat)
	}
}

func TestEnsureStagingInclude_RefusesCustomNonCoveringInclude(t *testing.T) {
	root := "[daemon]\ninclude = [\"other/*.toml\"]\n"
	_, _, err := EnsureStagingInclude([]byte(root), "/etc/runwisp")
	assert.ErrorIs(t, err, ErrIncludeNeedsManualWiring)
}

func TestEnsureStagingInclude_HandlesHeaderWithTrailingComment(t *testing.T) {
	root := "[daemon] # my daemon settings\nshutdown_timeout = \"10s\"\n"
	out, changed, err := EnsureStagingInclude([]byte(root), "/etc/runwisp")
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Contains(t, string(out), `include = ["runwisp.d/*.toml"]`)
}

func TestEnsureStagingInclude_AppendsWhenNoTrailingNewline(t *testing.T) {
	root := "[tasks.web]\nrun = \"echo hi\"" // no trailing newline
	out, _, err := EnsureStagingInclude([]byte(root), "/etc/runwisp")
	require.NoError(t, err)
	assert.NotContains(t, string(out), `hi"[daemon]`, "insertion must not fuse onto the last line")
	assert.Contains(t, string(out), "[daemon]")
}

// TestEnsureStagingInclude_WiredConfigLoads proves the surgically-wired root
// actually loads and picks up the staging file end-to-end.
func TestEnsureStagingInclude_WiredConfigLoads(t *testing.T) {
	dir := writeFileTree(t, map[string]string{
		"runwisp.toml":            "[tasks.native]\nrun = \"echo native\"\n",
		"runwisp.d/imported.toml": "[tasks.imported]\ncron = \"0 3 * * *\"\nrun = \"echo imported\"\n",
	})
	rootPath := filepath.Join(dir, "runwisp.toml")

	orig, err := os.ReadFile(rootPath)
	require.NoError(t, err)
	wired, changed, err := EnsureStagingInclude(orig, dir)
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, os.WriteFile(rootPath, wired, 0o600))

	cfg, err := Load(rootPath)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"native", "imported"}, taskNames(cfg))
	assert.True(t, findTask(t, cfg, "imported").Staged)
	assert.False(t, findTask(t, cfg, "native").Staged)
}

func TestEnsureStagingInclude_InvalidTOMLErrors(t *testing.T) {
	_, _, err := EnsureStagingInclude([]byte("[daemon\nbroken"), "/etc/runwisp")
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrIncludeNeedsManualWiring))
}
