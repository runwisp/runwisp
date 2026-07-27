// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunOnStart_ParsedOnTask(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.warm]
run = "echo warm"
run_on_start = true
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.True(t, findTask(t, cfg, "warm").RunOnStart)
}

func TestRunOnStart_DefaultsFalse(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.warm]
run = "echo warm"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.False(t, findTask(t, cfg, "warm").RunOnStart)
}

func TestRunOnStart_RejectedOnService(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[services.worker]
run = "sleep 1"
run_on_start = true
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "run_on_start")
}
