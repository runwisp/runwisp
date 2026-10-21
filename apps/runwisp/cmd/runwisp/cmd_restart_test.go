// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"testing"

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
