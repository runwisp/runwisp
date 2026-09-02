// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/config"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/runwisp/runwisp/internal/version"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUpdateHTTPClient_SetsTimeout(t *testing.T) {
	assert.Equal(t, 30*time.Second, updateHTTPClient().Timeout)
}

func TestUpdatesEnabled(t *testing.T) {
	origVersion := version.Version
	t.Cleanup(func() { version.Version = origVersion })

	tests := []struct {
		name         string
		checkUpdates bool
		ver          string
		want         bool
	}{
		{"disabled via config", false, "1.2.3", false},
		{"dev build never checks", true, "0.0.0-dev", false},
		{"enabled release build", true, "1.2.3", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			version.Version = tc.ver
			cfg := &daemonConfig{Config: &config.Config{Daemon: config.Daemon{CheckUpdates: tc.checkUpdates}}}
			assert.Equal(t, tc.want, updatesEnabled(cfg))
		})
	}
}

func TestStartUpdateChecker_DisabledReturnsNil(t *testing.T) {
	cfg := &daemonConfig{Config: &config.Config{Daemon: config.Daemon{CheckUpdates: false}}}
	assert.Nil(t, startUpdateChecker(cfg, nil))
}

func TestUpdateStatusHook(t *testing.T) {
	assert.Nil(t, updateStatusHook(nil))

	checker := runtime.NewUpdateChecker("0.16.0", nil, nil)
	hook := updateStatusHook(checker)
	require.NotNil(t, hook)
	available, latest := hook()
	assert.False(t, available)
	assert.Empty(t, latest)
}

func TestSelfUpdateHook_NilWhenUpdatesDisabled(t *testing.T) {
	origVersion := version.Version
	t.Cleanup(func() { version.Version = origVersion })
	version.Version = "0.0.0-dev" // never a release build in the test binary

	cfg := &daemonConfig{Config: &config.Config{Daemon: config.Daemon{CheckUpdates: true}}}
	assert.Nil(t, selfUpdateHook(cfg))
}

func TestRequestReexecRestart_SignalsSelfAndSetsFlag(t *testing.T) {
	t.Cleanup(func() { reexecRequested.Store(false) })

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	require.NoError(t, requestReexecRestart())
	assert.True(t, reexecRequested.Load())

	select {
	case <-sigCh:
	case <-time.After(5 * time.Second):
		t.Fatal("expected SIGTERM to be delivered to self")
	}
}

func TestMaybeReexec_NoopWhenNotRequested(t *testing.T) {
	reexecRequested.Store(false)
	assert.NoError(t, maybeReexec())
}

// TestRunDaemonAndReexec_PropagatesRunDaemonError mirrors
// TestRunDaemon_BadDBPathReturnsError: runDaemonAndReexec must surface a
// runDaemon failure as-is, without ever reaching maybeReexec.
func TestRunDaemonAndReexec_PropagatesRunDaemonError(t *testing.T) {
	f := Flags{
		DataDir: "/proc/runwisp-cannot-create",
		CfgFile: writeMinimalTOML(t),
	}
	err := runDaemonAndReexec(modeStandalone, f, true)
	require.Error(t, err)
}
