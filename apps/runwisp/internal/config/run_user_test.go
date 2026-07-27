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

func TestRunUser_ParsedOnTask(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run  = "echo hi"
user = "nobody:nogroup"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "nobody:nogroup", findTask(t, cfg, "job").RunUser)
}

func TestRunUser_ParsedOnService(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[services.worker]
run  = "sleep 1"
user = "nobody"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "nobody", findTask(t, cfg, "worker").RunUser)
}

func TestRunUser_RejectsMalformedSpec(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run  = "echo hi"
user = "a:b:c"
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "user for task job")
}

func TestRunUser_RejectsEmptyUser(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run  = "echo hi"
user = ":nogroup"
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty user")
}

func TestRunUser_RejectedOnCompose(t *testing.T) {
	cfgPath, dir := writePlainConfig(t, `[tasks.job]
compose_file    = "docker-compose.yml"
compose_service = "web"
user            = "nobody"
`)
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "docker-compose.yml"),
		[]byte("services:\n  web:\n    image: nginx\n"), 0644))

	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "user is not supported on compose")
}
