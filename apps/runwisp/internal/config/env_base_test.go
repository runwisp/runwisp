// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseEnvBase(t *testing.T) {
	// An omitted key resolves to a concrete value here rather than staying
	// zero, so nothing downstream has to know that "" means inherit.
	got, err := parseEnvBase("")
	require.NoError(t, err)
	assert.Equal(t, model.EnvBaseInherit, got)

	got, err = parseEnvBase("  clean  ")
	require.NoError(t, err)
	assert.Equal(t, model.EnvBaseClean, got)

	for _, bad := range []string{"cron", "empty", "Clean", "true"} {
		_, err := parseEnvBase(bad)
		assert.Error(t, err, "%q should be rejected", bad)
	}
}

func TestEnvBase_ParsedOnTask(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
cron     = "@daily"
run      = "/bin/job"
env_base = "clean"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, model.EnvBaseClean, findTask(t, cfg, "job").EnvBase)
}

// TestEnvBase_DefaultsToInherit is the compatibility half: every task that says
// nothing must keep the behaviour it had before the key existed.
func TestEnvBase_DefaultsToInherit(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
cron = "@daily"
run  = "/bin/job"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, model.EnvBaseInherit, findTask(t, cfg, "job").EnvBase)
}

func TestEnvBase_RejectsUnknownValue(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
cron     = "@daily"
run      = "/bin/job"
env_base = "cron"
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "env_base")
}

// TestEnvBase_RejectedOnCompose: a container never inherits the daemon's
// environment in the first place, so accepting the key there would let an
// operator write something that reads like it does anything.
func TestEnvBase_RejectedOnCompose(t *testing.T) {
	cfgPath, dir := writePlainConfig(t, `[tasks.job]
compose_file    = "docker-compose.yml"
compose_service = "web"
env_base        = "clean"
`)
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "docker-compose.yml"),
		[]byte("services:\n  web:\n    image: nginx\n"), 0644))

	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "env_base is not supported on compose")
}

// TestEnvBase_ReachesTheExecutionDef guards the wiring rather than the parse:
// the loader resolving the key is useless if the shell backend never sees it.
func TestEnvBase_ReachesTheExecutionDef(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
cron     = "@daily"
run      = "/bin/job"
env_base = "clean"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)

	def := findTask(t, cfg, "job").ResolvedExecutionDef()
	shell, ok := def.(*model.ShellExecution)
	require.True(t, ok, "expected a shell execution, got %T", def)
	assert.Equal(t, model.EnvBaseClean, shell.EnvBase)
}
