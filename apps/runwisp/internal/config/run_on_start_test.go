// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
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

func TestRunOnStart_Modes(t *testing.T) {
	cases := []struct {
		value string
		on    bool
		mode  model.RunOnStartMode
	}{
		{`true`, true, model.RunOnStartDaemon},
		{`false`, false, ""},
		{`"daemon"`, true, model.RunOnStartDaemon},
		{`"boot"`, true, model.RunOnStartBoot},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			cfgPath, _ := writePlainConfig(t, "[tasks.warm]\nrun = \"echo warm\"\nrun_on_start = "+tc.value+"\n")
			cfg, err := Load(cfgPath)
			require.NoError(t, err)
			task := findTask(t, cfg, "warm")
			assert.Equal(t, tc.on, task.RunOnStart)
			assert.Equal(t, tc.mode, task.RunOnStartMode)
		})
	}
}

func TestRunOnStart_RejectsUnknownValue(t *testing.T) {
	for _, value := range []string{`"reboot"`, `"Boot"`, `1`, `"true"`} {
		t.Run(value, func(t *testing.T) {
			cfgPath, _ := writePlainConfig(t, "[tasks.warm]\nrun = \"echo warm\"\nrun_on_start = "+value+"\n")
			_, err := Load(cfgPath)
			require.Error(t, err)
			assert.Contains(t, err.Error(), `task "warm" has invalid run_on_start`)
			assert.Contains(t, err.Error(), `valid values are true, false, "daemon", and "boot"`)
		})
	}
}
