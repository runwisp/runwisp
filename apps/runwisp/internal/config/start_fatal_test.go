// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthyAfter_Explicit(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[services.worker]
run = "sleep 1"
healthy_after = "30s"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, 30*time.Second, findTask(t, cfg, "worker").HealthyAfter)
}

func TestHealthyAfter_DefaultsToBuiltin(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[services.worker]
run = "sleep 1"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, DefaultHealthyAfter, findTask(t, cfg, "worker").HealthyAfter)
}

// TestHealthyAfter_RejectsCollapsedKeys is the bug-first guard for the
// start_period + backoff_reset_after → healthy_after collapse: both old keys
// are now unknown and rejected outright (pre-1.0, no shims).
func TestHealthyAfter_RejectsCollapsedKeys(t *testing.T) {
	for _, key := range []string{`start_period = "5s"`, `backoff_reset_after = "30s"`} {
		cfgPath, _ := writePlainConfig(t, `[services.worker]
run = "sleep 1"
`+key+"\n")
		_, err := Load(cfgPath)
		require.Errorf(t, err, "%s must be rejected as an unknown key", key)
	}
}

func TestStartRetries_DefaultsToBuiltin(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[services.worker]
run = "sleep 1"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, DefaultStartRetries, findTask(t, cfg, "worker").RestartAttempts)
}

func TestStartRetries_ExplicitWins(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[services.worker]
run = "sleep 1"
restart_attempts = 5
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	assert.Equal(t, 5, findTask(t, cfg, "worker").RestartAttempts)
}

func TestStartRetries_InheritsFromDefaults(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[defaults]
restart_attempts = 7
healthy_after = "15s"

[services.worker]
run = "sleep 1"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	svc := findTask(t, cfg, "worker")
	assert.Equal(t, 7, svc.RestartAttempts, "[defaults] restart_attempts is inherited")
	assert.Equal(t, 15*time.Second, svc.HealthyAfter, "[defaults] healthy_after is inherited")
}

func TestStartRetries_RejectedAboveCap(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[services.worker]
run = "sleep 1"
restart_attempts = 101
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cap")
}

func TestStartRetries_RejectedNegative(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[services.worker]
run = "sleep 1"
restart_attempts = -1
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "restart_attempts")
}

func TestStartFatalKeys_RejectedOnTask(t *testing.T) {
	for _, key := range []string{`healthy_after = "5s"`, `restart_attempts = 2`} {
		cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
`+key+"\n")
		_, err := Load(cfgPath)
		require.Errorf(t, err, "%s must be rejected on a task", key)
	}
}
