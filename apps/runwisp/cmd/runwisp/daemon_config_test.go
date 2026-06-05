// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"testing"
)

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
