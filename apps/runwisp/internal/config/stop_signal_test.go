// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

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

func TestStopSignal_CanonicalizesShortForm(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
stop_signal = "int"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, "SIGINT", findTask(t, cfg, "job").StopSignal)
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
