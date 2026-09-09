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
	require.NotNil(t, findTask(t, cfg, "worker").HealthyAfter)
	assert.Equal(t, 30*time.Second, *findTask(t, cfg, "worker").HealthyAfter)
}

func TestHealthyAfter_DefaultsToBuiltin(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[services.worker]
run = "sleep 1"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	require.NotNil(t, findTask(t, cfg, "worker").HealthyAfter)
	assert.Equal(t, DefaultHealthyAfter, *findTask(t, cfg, "worker").HealthyAfter)
}

// TestHealthyAfter_ExplicitZeroPreserved is the bug-first guard for the
// defaulting bug: an operator who writes `healthy_after = "0s"` (healthy the
// instant it starts) must get literal zero back, not have it silently
// overridden to DefaultHealthyAfter because 0 used to be indistinguishable
// from "omitted".
func TestHealthyAfter_ExplicitZeroPreserved(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[services.worker]
run = "sleep 1"
healthy_after = "0s"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	got := findTask(t, cfg, "worker").HealthyAfter
	require.NotNil(t, got)
	assert.Equal(t, time.Duration(0), *got, "explicit healthy_after = 0s must be preserved, not defaulted")
}

// TestRestartDelay_ExplicitZeroPreserved mirrors TestHealthyAfter_ExplicitZeroPreserved
// for restart_delay: an explicit "0s" (restart instantly, no delay) must not
// be silently overridden to the built-in 1s default.
func TestRestartDelay_ExplicitZeroPreserved(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[services.worker]
run = "sleep 1"
restart_delay = "0s"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	got := findTask(t, cfg, "worker").RestartDelay
	require.NotNil(t, got)
	assert.Equal(t, time.Duration(0), *got, "explicit restart_delay = 0s must be preserved, not defaulted")
}

// TestRestartDelay_DefaultsToBuiltin confirms the "normal" path is unaffected
// by the pointer conversion: omitting restart_delay still resolves to 1s.
func TestRestartDelay_DefaultsToBuiltin(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[services.worker]
run = "sleep 1"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	got := findTask(t, cfg, "worker").RestartDelay
	require.NotNil(t, got)
	assert.Equal(t, DefaultRestartDelay, *got)
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
	require.NotNil(t, findTask(t, cfg, "worker").RestartAttempts)
	assert.Equal(t, DefaultStartRetries, *findTask(t, cfg, "worker").RestartAttempts)
}

func TestStartRetries_ExplicitWins(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[services.worker]
run = "sleep 1"
restart_attempts = 5
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	require.NotNil(t, findTask(t, cfg, "worker").RestartAttempts)
	assert.Equal(t, 5, *findTask(t, cfg, "worker").RestartAttempts)
}

// TestStartRetries_ExplicitZeroPreserved is the bug-first guard for the
// defaulting bug: an operator who writes `restart_attempts = 0` (give up on
// the very first failure) must get literal zero back, not have it silently
// overridden to DefaultStartRetries because 0 used to be indistinguishable
// from "omitted".
func TestStartRetries_ExplicitZeroPreserved(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[services.worker]
run = "sleep 1"
restart_attempts = 0
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	got := findTask(t, cfg, "worker").RestartAttempts
	require.NotNil(t, got)
	assert.Equal(t, 0, *got, "explicit restart_attempts = 0 must be preserved, not defaulted")
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
	require.NotNil(t, svc.RestartAttempts)
	assert.Equal(t, 7, *svc.RestartAttempts, "[defaults] restart_attempts is inherited")
	require.NotNil(t, svc.HealthyAfter)
	assert.Equal(t, 15*time.Second, *svc.HealthyAfter, "[defaults] healthy_after is inherited")
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
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run = "echo hi"
healthy_after = "5s"
`)
	_, err := Load(cfgPath)
	require.Error(t, err, "healthy_after must be rejected on a task")
}

// TestTaskRestartAttempts_DefaultsToBuiltin mirrors TestStartRetries_* for
// [tasks.*]: a restarting task now gives up after the same number of
// consecutive failures a service would, instead of restarting forever.
func TestTaskRestartAttempts_DefaultsToBuiltin(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run     = "echo hi"
restart = "on_failure"
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	require.NotNil(t, findTask(t, cfg, "job").RestartAttempts)
	assert.Equal(t, DefaultStartRetries, *findTask(t, cfg, "job").RestartAttempts)
}

func TestTaskRestartAttempts_ExplicitWins(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run               = "echo hi"
restart           = "on_failure"
restart_attempts  = 5
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	require.NotNil(t, findTask(t, cfg, "job").RestartAttempts)
	assert.Equal(t, 5, *findTask(t, cfg, "job").RestartAttempts)
}

// TestTaskRestartAttempts_ExplicitZeroPreserved mirrors
// TestStartRetries_ExplicitZeroPreserved for [tasks.*]: an explicit
// restart_attempts = 0 must be preserved, not defaulted.
func TestTaskRestartAttempts_ExplicitZeroPreserved(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run               = "echo hi"
restart           = "on_failure"
restart_attempts  = 0
`)
	cfg, err := Load(cfgPath)
	require.NoError(t, err)
	got := findTask(t, cfg, "job").RestartAttempts
	require.NotNil(t, got)
	assert.Equal(t, 0, *got, "explicit restart_attempts = 0 must be preserved, not defaulted")
}

func TestTaskRestartAttempts_RejectedAboveCap(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run               = "echo hi"
restart_attempts  = 101
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cap")
}

func TestTaskRestartAttempts_RejectedNegative(t *testing.T) {
	cfgPath, _ := writePlainConfig(t, `[tasks.job]
run               = "echo hi"
restart_attempts  = -1
`)
	_, err := Load(cfgPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "restart_attempts")
}
