// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStopSignal_DefaultsToSIGTERM(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, DefaultStopSignal, findTask(t, cfg, "job").StopSignal)
}

func TestStopSignal_CanonicalizesCase(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
stop_signal = "sigint"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "SIGINT", findTask(t, cfg, "job").StopSignal)
}

func TestStopSignal_BareFormRejected(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
stop_signal = "INT"
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stop_signal for task job")
}

func TestStopSignal_InheritedAndOverridden(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[defaults]
stop_signal = "SIGQUIT"

[tasks.inherits]
run = "echo hi"

[tasks.overrides]
run = "echo hi"
stop_signal = "SIGUSR1"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "SIGQUIT", findTask(t, cfg, "inherits").StopSignal)
	assert.Equal(t, "SIGUSR1", findTask(t, cfg, "overrides").StopSignal)
}

func TestStopSignal_BogusIsRejected(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
stop_signal = "SIGNOPE"
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stop_signal for task job")
}

func TestDefaults_InheritSupervisionAndCatchupKeys(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[defaults]
graceful_stop = "12s"
catch_up = 7
restart_delay = "3s"
restart_backoff = "linear"

[tasks.cronjob]
cron = "* * * * *"
on_overlap = "queue"
run = "echo hi"

[services.web]
run = "exec ./web"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)

	cron := findTask(t, cfg, "cronjob")
	assert.Equal(t, 12*time.Second, cron.GracefulStopValue())
	assert.Equal(t, 7, cron.CatchUpValue())

	web := findTask(t, cfg, "web")
	assert.Equal(t, 12*time.Second, web.GracefulStopValue())
	require.NotNil(t, web.RestartDelay)
	assert.Equal(t, 3*time.Second, *web.RestartDelay)
	assert.Equal(t, model.BackoffLinear, web.RestartBackoff)
}

func TestStopSignal_DefaultsBogusIsRejected(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[defaults]
stop_signal = "BANANA"

[tasks.job]
run = "echo hi"
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "defaults.stop_signal")
}
