// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPriority_ParsedOnService(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[services.worker]
run = "sleep 1"
priority = 10
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, 10, findTask(t, cfg, "worker").Priority)
}

func TestPriority_RejectedOnTask(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
priority = 5
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "priority")
}

func TestAutostart_DefaultsToTrue(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[services.worker]
run = "sleep 1"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.True(t, findTask(t, cfg, "worker").Autostart)
}

func TestAutostart_ExplicitFalse(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[services.worker]
run = "sleep 1"
autostart = false
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.False(t, findTask(t, cfg, "worker").Autostart)
}

func TestAutostart_RejectedOnTask(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
autostart = false
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "autostart")
}
