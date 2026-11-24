// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/config"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRestartRefusesAnAppDaemon guards an SDK app's daemon: restarted from
// the CLI it would come back reading runwisp.toml, not the app's config.
func TestRestartRefusesAnAppDaemon(t *testing.T) {
	assert.Error(t, appDaemonRestartError(&model.InstanceInfo{ConfigSource: "app"}))
	assert.NoError(t, appDaemonRestartError(&model.InstanceInfo{ConfigSource: "file"}))
	assert.NoError(t, appDaemonRestartError(nil))
}

// TestValidateRestartConfigRejectsABrokenFile guards a restart against a
// typo: the fresh daemon would exit on it, so the old one must stay up.
func TestValidateRestartConfigRejectsABrokenFile(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.toml")
	require.NoError(t, os.WriteFile(bad, []byte("[tasks.x]\nrun = \"true\"\ntimeout = \"10 minutes\"\n"), 0o600))
	good := filepath.Join(dir, "good.toml")
	require.NoError(t, os.WriteFile(good, []byte("[tasks.x]\nrun = \"true\"\n"), 0o600))

	err := validateRestartConfig(bad)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "left running")
	assert.NoError(t, validateRestartConfig(good))
}

func TestRunningConfigPathPrefersTheDaemonsReport(t *testing.T) {
	f := Flags{CfgFile: "from-flags.toml"}
	assert.Equal(t, "from-flags.toml", runningConfigPath(nil, f))
	assert.Equal(t, "from-flags.toml", runningConfigPath(&model.InstanceInfo{}, f))
	assert.Equal(t, "/etc/rw.toml", runningConfigPath(&model.InstanceInfo{ConfigPath: "/etc/rw.toml"}, f))
}

func TestIsAppDaemon(t *testing.T) {
	assert.True(t, isAppDaemon(&model.InstanceInfo{ConfigSource: "app"}))
	assert.False(t, isAppDaemon(&model.InstanceInfo{ConfigSource: "file"}))
	assert.False(t, isAppDaemon(nil))
}

func TestRunRestartRejectsAttachWithoutTarget(t *testing.T) {
	err := runRestart(restartCmd, nil, Flags{}, true)
	assert.ErrorIs(t, err, errAttachNeedsTarget)
}

func TestRunRestartNeedsATargetForRemoteOrToken(t *testing.T) {
	t.Cleanup(func() { controlRemote = remoteFlags{} })

	controlRemote = remoteFlags{URL: "http://127.0.0.1:1"}
	err := runRestart(restartCmd, nil, Flags{}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--url needs a target")

	controlRemote = remoteFlags{Token: "tok"}
	err = runRestart(restartCmd, nil, Flags{}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--token needs a target")
}

func TestRunningDaemonInfoIsNilWhenNothingRuns(t *testing.T) {
	info, err := runningDaemonInfo(restartCmd, Flags{}, false)
	assert.NoError(t, err)
	assert.Nil(t, info)
}

func TestPrintRestartedFallsBackWithoutAConfig(t *testing.T) {
	var out bytes.Buffer
	printRestarted(&out, Flags{DataDir: t.TempDir(), CfgFile: filepath.Join(t.TempDir(), "missing.toml")})
	assert.Equal(t, "Daemon restarted.\n", out.String())
}

func TestPrintRestartedPointsAtTheWebUI(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "runwisp.toml")
	require.NoError(t, os.WriteFile(cfg, []byte("[tasks.x]\nrun = \"true\"\n"), 0o600))
	var out bytes.Buffer
	printRestarted(&out, Flags{DataDir: t.TempDir(), CfgFile: cfg})
	assert.Contains(t, out.String(), "web UI at ")
}

func TestDaemonListenURLIsEmptyWithoutHTTP(t *testing.T) {
	cfg := &config.Config{Daemon: config.Daemon{ExternalURL: "https://cron.example.com"}}
	assert.Empty(t, daemonListenURL(cfg, Flags{NoHTTP: true, Port: 9477}))
}
