// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadTOML(t *testing.T, body string) (*Config, error) {
	t.Helper()
	dir := writeFileTree(t, map[string]string{"runwisp.toml": body})
	return Load(filepath.Join(dir, "runwisp.toml"))
}

func TestSDKTaskAndService(t *testing.T) {
	cfg, err := loadTOML(t, `
[tasks.digest]
cron = "0 8 * * *"
sdk = true
env = { REGION = "eu" }

[services.worker]
sdk = true
`)
	require.NoError(t, err)
	byName := map[string]*model.Task{}
	for i := range cfg.Tasks {
		byName[cfg.Tasks[i].Name] = &cfg.Tasks[i]
	}
	assert.Equal(t, &model.SDKExecution{}, byName["digest"].ResolvedExecutionDef())
	assert.Equal(t, map[string]string{"REGION": "eu"}, byName["digest"].Env)
	assert.Equal(t, &model.SDKExecution{}, byName["worker"].ResolvedExecutionDef())
}

func TestSDKRejectsProcessKeys(t *testing.T) {
	for _, key := range []string{
		`run = "true"`,
		`compose_file = "compose.yml"`,
		`shell = "/bin/bash"`,
		`umask = "077"`,
		`user = "nobody"`,
		`env_base = "clean"`,
		`working_dir = "/tmp"`,
		`stop_signal = "SIGINT"`,
	} {
		t.Run(key, func(t *testing.T) {
			_, err := loadTOML(t, "[tasks.a]\nsdk = true\n"+key+"\n")
			require.Error(t, err)
			assert.Contains(t, err.Error(), "sets both sdk and")
		})
	}
}

// sdk = false is a unit without it: run is required as usual.
func TestSDKFalseNeedsRun(t *testing.T) {
	_, err := loadTOML(t, "[tasks.a]\nsdk = false\n")
	require.Error(t, err)
}
