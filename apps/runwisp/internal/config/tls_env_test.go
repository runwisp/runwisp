// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadRUNWISPTLSOverride(t *testing.T) {
	t.Run("unset leaves TOML value untouched", func(t *testing.T) {
		path := writeTOML(t, `
[daemon]
tls = "off"

[tasks.t]
run = "/bin/true"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, TLSModeOff, cfg.Daemon.TLS)
	})

	t.Run("unset leaves default off untouched", func(t *testing.T) {
		path := writeTOML(t, `
[tasks.t]
run = "/bin/true"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, TLSModeOff, cfg.Daemon.TLS)
	})

	t.Run("env off overrides TOML auto", func(t *testing.T) {
		t.Setenv("RUNWISP_TLS", "off")
		path := writeTOML(t, `
[daemon]
tls = "auto"

[tasks.t]
run = "/bin/true"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, TLSModeOff, cfg.Daemon.TLS)
	})

	t.Run("env auto overrides TOML off", func(t *testing.T) {
		t.Setenv("RUNWISP_TLS", "auto")
		path := writeTOML(t, `
[daemon]
tls = "off"

[tasks.t]
run = "/bin/true"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, TLSModeAuto, cfg.Daemon.TLS)
	})

	t.Run("value is case-insensitive and trimmed", func(t *testing.T) {
		t.Setenv("RUNWISP_TLS", "  OFF  ")
		path := writeTOML(t, `
[tasks.t]
run = "/bin/true"
`)
		cfg, err := Load(path)
		require.NoError(t, err)
		assert.Equal(t, TLSModeOff, cfg.Daemon.TLS)
	})

	t.Run("invalid value is a load error", func(t *testing.T) {
		t.Setenv("RUNWISP_TLS", "nonsense")
		path := writeTOML(t, `
[tasks.t]
run = "/bin/true"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "RUNWISP_TLS")
	})
}
