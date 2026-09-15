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

// TestValidateTLSOffCertContradiction covers the tls = "off" + tls_cert/tls_key
// combination: resolveTLS and tlsScheme (cmd/runwisp/daemon_tls.go) both decide
// HTTPS purely from the cert/key being present, so an explicit tls = "off"
// alongside them was previously silently overridden into HTTPS with no error.
// validateTLS now rejects that contradiction outright, while leaving the
// documented "just set tls_cert/tls_key, don't mention tls at all" flow (and
// the redundant tls = "auto" + cert/key one) working as before.
func TestValidateTLSOffCertContradiction(t *testing.T) {
	t.Run("explicit off with cert and key is rejected", func(t *testing.T) {
		err := validateTLS(&Daemon{TLS: TLSModeOff, TLSCert: "/nonexistent/cert.pem", TLSKey: "/nonexistent/key.pem"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `tls = "off" cannot be combined with tls_cert/tls_key`)
	})

	t.Run("unset tls with cert and key is not treated as the off contradiction", func(t *testing.T) {
		err := validateTLS(&Daemon{TLSCert: "/nonexistent/cert.pem", TLSKey: "/nonexistent/key.pem"})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "cannot be combined")
		assert.Contains(t, err.Error(), "tls_cert/tls_key")
	})

	t.Run("auto with cert and key is not treated as a contradiction", func(t *testing.T) {
		err := validateTLS(&Daemon{TLS: TLSModeAuto, TLSCert: "/nonexistent/cert.pem", TLSKey: "/nonexistent/key.pem"})
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "cannot be combined")
	})

	t.Run("ApplyDefaults leaves tls unset when a cert and key are already set", func(t *testing.T) {
		cfg := &Config{Daemon: Daemon{TLSCert: "/nonexistent/cert.pem", TLSKey: "/nonexistent/key.pem"}}
		ApplyDefaults(cfg)
		assert.Empty(t, cfg.Daemon.TLS)
	})

	t.Run("ApplyDefaults still defaults tls to off without a cert", func(t *testing.T) {
		cfg := &Config{}
		ApplyDefaults(cfg)
		assert.Equal(t, TLSModeOff, cfg.Daemon.TLS)
	})

	t.Run("full Load rejects explicit off combined with cert and key", func(t *testing.T) {
		path := writeTOML(t, `
[daemon]
tls = "off"
tls_cert = "/nonexistent/cert.pem"
tls_key = "/nonexistent/key.pem"

[tasks.t]
run = "/bin/true"
`)
		_, err := Load(path)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `tls = "off" cannot be combined with tls_cert/tls_key`)
	})
}
