// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeMinimalTOML(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "runwisp.toml")
	body := `
[tasks.example]
run = "echo hi"
`
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestLoadConfigFile_MissingWithStationReturnsDefaults(t *testing.T) {
	cfg, err := loadConfigFile("/this/does/not/exist/runwisp.toml", true)
	require.NoError(t, err)
	require.NotNil(t, cfg)
}

func TestLoadConfigFile_MissingWithoutStationErrors(t *testing.T) {
	_, err := loadConfigFile("/this/does/not/exist/runwisp.toml", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no runwisp.toml")

	// It never silently swaps in a fallback config — the operator gets a
	// userFacingError naming both ways forward instead.
	ufe, ok := isUserFacing(err)
	require.True(t, ok, "expected a *userFacingError")
	assert.Contains(t, ufe.details, "runwisp demo")
	assert.Contains(t, ufe.details, "docs.runwisp.com")
}

// loadDaemonConfig integrates loadConfigFile + fingerprint resolution +
// resolvePassword + auth.DeriveSessionKey. We exercise the standalone path with a
// stable RUNWISP_PASSWORD so PasswordEphemeral is deterministic.
func TestLoadDaemonConfig_StandaloneWithStablePassword(t *testing.T) {
	t.Setenv("RUNWISP_PASSWORD", "stable-test-secret")
	t.Setenv("RUNWISP_FINGERPRINT", "test-fp-123")

	f := Flags{CfgFile: writeMinimalTOML(t)}

	db, err := storage.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cfg, err := loadDaemonConfig(t.Context(), db, modeStandalone, f)
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "test-fp-123", cfg.Fingerprint)
	assert.Equal(t, "stable-test-secret", cfg.Password)
	assert.False(t, cfg.PasswordEphemeral, "env password must not be ephemeral")
	assert.NotEmpty(t, cfg.SessionKey)
	require.Len(t, cfg.Config.Tasks, 1)
	assert.Equal(t, "example", cfg.Config.Tasks[0].Name)
	assert.False(t, cfg.StationConfig.Enabled)
}

func TestLoadDaemonConfig_StandaloneEphemeralPassword(t *testing.T) {
	require.NoError(t, os.Unsetenv("RUNWISP_PASSWORD"))
	t.Setenv("RUNWISP_FINGERPRINT", "eph-fp")

	f := Flags{CfgFile: writeMinimalTOML(t)}

	db, err := storage.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	cfg, err := loadDaemonConfig(t.Context(), db, modeStandalone, f)
	require.NoError(t, err)
	assert.True(t, cfg.PasswordEphemeral)
	assert.NotEmpty(t, cfg.Password)
}

func TestLoadDaemonConfig_MissingTOMLInStandaloneErrors(t *testing.T) {
	t.Setenv("RUNWISP_PASSWORD", "x")
	t.Setenv("RUNWISP_FINGERPRINT", "fp")

	f := Flags{CfgFile: filepath.Join(t.TempDir(), "missing.toml")}

	db, err := storage.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = loadDaemonConfig(t.Context(), db, modeStandalone, f)
	assert.Error(t, err)
}

func TestLoadDaemonConfig_FingerprintPersistsAcrossCalls(t *testing.T) {
	require.NoError(t, os.Unsetenv("RUNWISP_FINGERPRINT"))
	t.Setenv("RUNWISP_PASSWORD", "stable")

	f := Flags{CfgFile: writeMinimalTOML(t)}

	db, err := storage.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	first, err := loadDaemonConfig(t.Context(), db, modeStandalone, f)
	require.NoError(t, err)
	require.NotEmpty(t, first.Fingerprint)

	second, err := loadDaemonConfig(t.Context(), db, modeStandalone, f)
	require.NoError(t, err)
	assert.Equal(t, first.Fingerprint, second.Fingerprint,
		"fingerprint persisted to DB on first call must be returned on subsequent calls")
}

// TestResolvePassword_EnvVarUsedInMemory guards the contract that when
// RUNWISP_PASSWORD is set, the value is returned in memory only and
// ephemeral=false (so auth.DeriveSessionKey yields a stable session key).
func TestResolvePassword_EnvVarUsedInMemory(t *testing.T) {
	t.Setenv("RUNWISP_PASSWORD", "from-env-secret")

	got, ephemeral, err := resolvePassword()
	if err != nil {
		t.Fatal(err)
	}
	if got != "from-env-secret" {
		t.Fatalf("expected env value, got %q", got)
	}
	if ephemeral {
		t.Fatal("env-supplied password must not be reported as ephemeral")
	}
}

// TestResolvePassword_EphemeralWhenEnvAbsent verifies that with no env var,
// a fresh in-memory password is minted and flagged as ephemeral. Sessions
// then rotate every boot because auth.DeriveSessionKey keys off the password.
func TestResolvePassword_EphemeralWhenEnvAbsent(t *testing.T) {
	if err := os.Unsetenv("RUNWISP_PASSWORD"); err != nil {
		t.Fatal(err)
	}

	got, ephemeral, err := resolvePassword()
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("expected non-empty ephemeral password")
	}
	if !ephemeral {
		t.Fatal("expected ephemeral=true when RUNWISP_PASSWORD is unset")
	}
}

// TestResolveAuthMode_Values locks the parse contract: only the unambiguous
// "1"/"true" (case-insensitive) enable no-auth; anything else non-empty is a
// startup error rather than a guess.
func TestResolveAuthMode_Values(t *testing.T) {
	tests := []struct {
		value   string
		noAuth  bool
		wantErr bool
	}{
		{"", false, false},
		{"off", true, false},
		{"OFF", true, false},
		{" off ", true, false},
		{"on", false, false},
		{"ON", false, false},
		{"1", false, true},
		{"true", false, true},
		{"0", false, true},
		{"yes", false, true},
	}
	for _, tt := range tests {
		t.Run("value="+tt.value, func(t *testing.T) {
			t.Setenv("RUNWISP_AUTH", tt.value)
			t.Setenv("RUNWISP_PASSWORD", "")

			noAuth, err := resolveAuthMode()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for RUNWISP_AUTH=%q", tt.value)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if noAuth != tt.noAuth {
				t.Fatalf("RUNWISP_AUTH=%q: expected noAuth=%v, got %v", tt.value, tt.noAuth, noAuth)
			}
		})
	}
}

// TestResolveAuthMode_ConflictWithPassword rejects the contradictory combo of
// a configured password and disabled auth — a password that is never checked
// gives a false sense of security.
func TestResolveAuthMode_ConflictWithPassword(t *testing.T) {
	t.Setenv("RUNWISP_AUTH", "off")
	t.Setenv("RUNWISP_PASSWORD", "some-password")

	if _, err := resolveAuthMode(); err == nil {
		t.Fatal("expected error when RUNWISP_AUTH=off and RUNWISP_PASSWORD are both set")
	}
}
