// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/runwisp/runwisp/internal/chap"
	"github.com/runwisp/runwisp/internal/storage"
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

// fakeConfigRepo is an in-memory ConfigRepository for resolveConfigValue tests.
type fakeConfigRepo struct {
	store  map[string]string
	getErr error
	setErr error
}

func (f *fakeConfigRepo) GetConfigValue(_ context.Context, k string) (string, bool, error) {
	if f.getErr != nil {
		return "", false, f.getErr
	}
	v, ok := f.store[k]
	return v, ok, nil
}

func (f *fakeConfigRepo) SetConfigValue(_ context.Context, k, v string) error {
	if f.setErr != nil {
		return f.setErr
	}
	if f.store == nil {
		f.store = map[string]string{}
	}
	f.store[k] = v
	return nil
}

func TestResolveConfigValue_EnvOverridesDB(t *testing.T) {
	t.Setenv("RUNWISP_TEST_KEY", "from-env")
	repo := &fakeConfigRepo{store: map[string]string{"key": "from-db"}}

	v, err := resolveConfigValue(t.Context(), repo, "key", "RUNWISP_TEST_KEY", func() (string, error) {
		t.Fatal("generate must not run when env override is set")
		return "", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "from-env", v)
}

func TestResolveConfigValue_ReadsFromDB(t *testing.T) {
	repo := &fakeConfigRepo{store: map[string]string{"key": "from-db"}}
	v, err := resolveConfigValue(t.Context(), repo, "key", "RUNWISP_MISSING_KEY", func() (string, error) {
		t.Fatal("generate must not run when DB has the key")
		return "", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "from-db", v)
}

func TestResolveConfigValue_GeneratesAndPersists(t *testing.T) {
	repo := &fakeConfigRepo{}
	called := 0
	v, err := resolveConfigValue(t.Context(), repo, "key", "RUNWISP_MISSING_KEY", func() (string, error) {
		called++
		return "newly-generated", nil
	})
	require.NoError(t, err)
	assert.Equal(t, "newly-generated", v)
	assert.Equal(t, 1, called)
	assert.Equal(t, "newly-generated", repo.store["key"])
}

func TestResolveConfigValue_DBErrorPropagates(t *testing.T) {
	repo := &fakeConfigRepo{getErr: errors.New("db read failure")}
	_, err := resolveConfigValue(t.Context(), repo, "key", "RUNWISP_MISSING_KEY", nil)
	assert.Error(t, err)
}

func TestResolveConfigValue_GenerateErrorPropagates(t *testing.T) {
	repo := &fakeConfigRepo{}
	_, err := resolveConfigValue(t.Context(), repo, "key", "RUNWISP_MISSING_KEY", func() (string, error) {
		return "", errors.New("generate failed")
	})
	assert.Error(t, err)
}

func TestResolveConfigValue_PersistErrorPropagates(t *testing.T) {
	repo := &fakeConfigRepo{setErr: errors.New("write failed")}
	_, err := resolveConfigValue(t.Context(), repo, "key", "RUNWISP_MISSING_KEY", func() (string, error) {
		return "ok", nil
	})
	assert.Error(t, err)
}

func TestResolveConfigValue_BlankEnvFallsThrough(t *testing.T) {
	t.Setenv("RUNWISP_BLANK_KEY", "   ") // whitespace-only counts as empty
	repo := &fakeConfigRepo{store: map[string]string{"k": "db"}}
	v, err := resolveConfigValue(t.Context(), repo, "k", "RUNWISP_BLANK_KEY", nil)
	require.NoError(t, err)
	assert.Equal(t, "db", v)
}

func TestLoadConfigFile_MissingWithCloudReturnsDefaults(t *testing.T) {
	cfg, _, err := loadConfigFile("/this/does/not/exist/runwisp.toml", true)
	require.NoError(t, err)
	require.NotNil(t, cfg)
}

func TestLoadConfigFile_MissingWithoutCloudErrors(t *testing.T) {
	_, _, err := loadConfigFile("/this/does/not/exist/runwisp.toml", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no runwisp.toml")
}

// loadDaemonConfig integrates loadConfigFile + resolveConfigValue +
// resolvePassword + deriveJWTSecret. We exercise the standalone path with a
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
	assert.NotEmpty(t, cfg.JWTSecret)
	require.Len(t, cfg.Config.Tasks, 1)
	assert.Equal(t, "example", cfg.Config.Tasks[0].Name)
	assert.False(t, cfg.CloudConfig.Enabled)
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
// ephemeral=false (so deriveJWTSecret yields a stable JWT key).
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
// then rotate every boot because deriveJWTSecret keys off the password.
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
		{"1", true, false},
		{"true", true, false},
		{"TRUE", true, false},
		{" true ", true, false},
		{"0", false, true},
		{"yes", false, true},
		{"on", false, true},
	}
	for _, tt := range tests {
		t.Run("value="+tt.value, func(t *testing.T) {
			t.Setenv("RUNWISP_NO_AUTH", tt.value)
			t.Setenv("RUNWISP_PASSWORD", "")

			noAuth, err := resolveAuthMode()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for RUNWISP_NO_AUTH=%q", tt.value)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if noAuth != tt.noAuth {
				t.Fatalf("RUNWISP_NO_AUTH=%q: expected noAuth=%v, got %v", tt.value, tt.noAuth, noAuth)
			}
		})
	}
}

// TestResolveAuthMode_ConflictWithPassword rejects the contradictory combo of
// a configured password and disabled auth — a password that is never checked
// gives a false sense of security.
func TestResolveAuthMode_ConflictWithPassword(t *testing.T) {
	t.Setenv("RUNWISP_NO_AUTH", "1")
	t.Setenv("RUNWISP_PASSWORD", "some-password")

	if _, err := resolveAuthMode(); err == nil {
		t.Fatal("expected error when RUNWISP_NO_AUTH and RUNWISP_PASSWORD are both set")
	}
}

// TestDeriveJWTSecret_DeterministicWithSameInputs is the property that lets
// browser sessions survive a daemon restart when RUNWISP_PASSWORD is stable.
func TestDeriveJWTSecret_DeterministicWithSameInputs(t *testing.T) {
	a, err := deriveJWTSecret("secret", "alpha-fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	b, err := deriveJWTSecret("secret", "alpha-fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("deriveJWTSecret must be deterministic; got %q vs %q", a, b)
	}
	if a == "" {
		t.Fatal("derived secret must not be empty")
	}
}

// TestDeriveJWTSecret_RotatesWhenPasswordChanges is the property that makes
// changing RUNWISP_PASSWORD invalidate every prior session. The new password
// derives a fresh JWT key, so old JWTs signed with the previous key fail.
func TestDeriveJWTSecret_RotatesWhenPasswordChanges(t *testing.T) {
	a, err := deriveJWTSecret("password-one", "alpha-fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	b, err := deriveJWTSecret("password-two", "alpha-fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("changing the password must change the derived JWT secret")
	}
}

// TestDeriveJWTSecret_PerInstallFingerprintSalt guards against cross-host
// JWT reuse: the same password on another machine/cwd produces a different
// key, so a leaked password alone doesn't let an attacker mint sessions for
// a different RunWisp install.
func TestDeriveJWTSecret_PerInstallFingerprintSalt(t *testing.T) {
	a, err := deriveJWTSecret("shared-password", "host-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := deriveJWTSecret("shared-password", "host-b")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("different fingerprint salts must yield different JWT secrets")
	}
}

// TestDeriveJWTSecret_UsesExpensiveKDF pins the derivation to PBKDF2 at
// chap.Iterations. The fingerprint salt is built from non-secret inputs, so the
// signing key's resistance to recovery rests on the password entropy plus the
// KDF cost. The JWT rides the same cleartext channel as the CHAP transcript on
// TLS-less deployments; a captured JWT must not be a cheaper offline oracle for
// the password than the CHAP transcript is. A regression to a fast single-pass
// KDF (the previous HKDF) would silently reopen that shortcut, so we assert the
// exact expected PBKDF2 output.
func TestDeriveJWTSecret_UsesExpensiveKDF(t *testing.T) {
	got, err := deriveJWTSecret("secret", "alpha-fingerprint")
	if err != nil {
		t.Fatal(err)
	}
	salt := []byte(jwtKDFInfo + "\x00" + "alpha-fingerprint")
	key, err := pbkdf2.Key(sha256.New, "secret", salt, chap.Iterations, 32)
	if err != nil {
		t.Fatal(err)
	}
	want := base64.RawURLEncoding.EncodeToString(key)
	if got != want {
		t.Fatalf("deriveJWTSecret must use PBKDF2 at chap.Iterations; got %q want %q", got, want)
	}
}
