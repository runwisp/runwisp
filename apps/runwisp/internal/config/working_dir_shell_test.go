// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writePlainConfig stages a runwisp.toml under a temp dir and returns the
// config path plus the dir, for exercising the full Load pipeline without a
// compose file.
func writePlainConfig(t *testing.T, toml string) (cfgPath, dir string) {
	t.Helper()
	dir = t.TempDir()
	cfgPath = filepath.Join(dir, "runwisp.toml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(toml), 0644))
	return cfgPath, dir
}

func TestWorkingDir_RelativeResolvesAgainstBaseDir(t *testing.T) {
	cfgPath, dir := writePlainConfig(t, `[tasks.job]
run = "echo hi"
working_dir = "sub"
`)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "sub"), 0755))

	cfg, err := Load(cfgPath)
	require.NoError(t, err)

	task := findTask(t, cfg, "job")
	assert.Equal(t, filepath.Join(dir, "sub"), task.WorkingDir)
	assert.True(t, filepath.IsAbs(task.WorkingDir))
}

// TestWorkingDir_MissingDirLoadsAndResolves proves existence is deferred to run
// time: a working_dir that does not exist still loads (and is absolutized), the
// same way an unresolvable shell isn't stat-ed at load. The executor surfaces
// the missing-dir failure at start — see TestShellBackend_MissingWorkingDir*.
func TestWorkingDir_MissingDirLoadsAndResolves(t *testing.T) {
	cfgPath, dir := writePlainConfig(t, `[tasks.job]
run = "echo hi"
working_dir = "does-not-exist"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "does-not-exist"), findTask(t, cfg, "job").WorkingDir)
}

func TestShell_DefaultsToBinSh(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, DefaultShell, findTask(t, cfg, "job").Shell)
}

func TestShell_DefaultsInheritedAndOverridden(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[defaults]
shell = "/bin/bash"

[tasks.inherits]
run = "echo hi"

[tasks.overrides]
run = "echo hi"
shell = "/usr/bin/zsh"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "/bin/bash", findTask(t, cfg, "inherits").Shell)
	assert.Equal(t, "/usr/bin/zsh", findTask(t, cfg, "overrides").Shell)
}

func TestShell_RelativePathIsRejected(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
shell = "bash"
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be an absolute path")
}

func TestShell_RejectedOnCompose(t *testing.T) {
	cfgPath, dir := writePlainConfig(t, `[tasks.job]
compose_file    = "docker-compose.yml"
compose_service = "web"
shell           = "/bin/bash"
`)
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "docker-compose.yml"),
		[]byte("services:\n  web:\n    image: nginx\n"), 0644))

	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "shell is not supported on compose")
}

// TestWorkingDir_TildeResolvesToTheDaemonsHomeWithoutAUser is the ordinary case:
// with no `user`, the task runs as the daemon's own account and `~` is that
// account's home — resolvable here, and checkable by `runwisp validate`.
func TestWorkingDir_TildeResolvesToTheDaemonsHomeWithoutAUser(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
working_dir = "~"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)

	home, err := os.UserHomeDir()
	require.NoError(t, err)
	assert.Equal(t, home, findTask(t, cfg, "job").WorkingDir)
}

// TestWorkingDir_TildeStaysLiteralForARunAsUser is why a system crontab is
// importable. `~` on a task that drops to another user means that user's home,
// and resolving it here would resolve it against the daemon's — pointing
// deploy's job at root's home, and making `runwisp validate` on a laptop
// disagree with the daemon on the server. It stays literal for the executor,
// which resolves it from the credential it looks up per run.
func TestWorkingDir_TildeStaysLiteralForARunAsUser(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
user = "deploy"
working_dir = "~"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "~", findTask(t, cfg, "job").WorkingDir,
		"a ~ on a run-as task must reach the executor unresolved")
}

// TestWorkingDir_TildeSubpathStaysLiteralForARunAsUser: the join happens in the
// executor too, so `~/logs` can't be half-resolved here.
func TestWorkingDir_TildeSubpathStaysLiteralForARunAsUser(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
user = "deploy"
working_dir = "~/logs"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "~/logs", findTask(t, cfg, "job").WorkingDir)
}

// TestWorkingDir_RelativeStillResolvesForARunAsUser keeps the exemption narrow:
// only a leading `~` is the run user's business. A relative path still resolves
// against the config's directory, which has nothing to do with who runs it.
func TestWorkingDir_RelativeStillResolvesForARunAsUser(t *testing.T) {
	cfgPath, dir := writePlainConfig(t, `[tasks.job]
run = "echo hi"
user = "deploy"
working_dir = "sub"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, "sub"), findTask(t, cfg, "job").WorkingDir)
}
