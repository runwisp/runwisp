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

func TestAutostart_Task(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		want    bool
		wantErr string
	}{
		{name: "defaults to true", body: `cron = "0 3 * * *"`, want: true},
		{name: "false on a cron task", body: "cron = \"0 3 * * *\"\nautostart = false", want: false},
		{name: "explicit true without cron", body: `autostart = true`, want: true},
		{name: "false needs cron", body: `autostart = false`, wantErr: "has no cron"},
		{
			name:    "false with manual_trigger = false",
			body:    "cron = \"0 3 * * *\"\nautostart = false\nmanual_trigger = false",
			wantErr: "could never be resumed",
		},
		{
			name:    "false with run_on_start",
			body:    "cron = \"0 3 * * *\"\nautostart = false\nrun_on_start = true",
			wantErr: "run_on_start",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath, _ := writePlainConfig(t, "[tasks.job]\nrun = \"echo hi\"\n"+tc.body+"\n")
			cfg, err := Load(cfgPath)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), `task "job" sets autostart = false`)
				assert.Contains(t, err.Error(), tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, findTask(t, cfg, "job").Autostart)
		})
	}
}
