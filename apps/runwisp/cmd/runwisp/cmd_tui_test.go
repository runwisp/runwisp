// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"
)

// TestResolveTUIAuthSource_LocalJWTByDefault confirms the TUI takes the
// local-JWT path when no password material is available. This is the everyday
// case: an operator running `runwisp tui` against their own daemon.
func TestResolveTUIAuthSource_LocalJWTByDefault(t *testing.T) {
	prev := tuiPasswordStdin
	t.Cleanup(func() { tuiPasswordStdin = prev })
	tuiPasswordStdin = false
	t.Setenv("RUNWISP_PASSWORD", "")

	src := resolveTUIAuthSource()
	if src.remote() {
		t.Fatalf("expected local-JWT path, got remote (stdin=%v env=%v)", src.useStdin, src.envSet)
	}
}

// TestResolveTUIAuthSource_RemoteOnEnv confirms that RUNWISP_PASSWORD alone
// flips the TUI to the SRP path even without --password-stdin. This is the
// docker/systemd `LoadCredential=` workflow.
func TestResolveTUIAuthSource_RemoteOnEnv(t *testing.T) {
	prev := tuiPasswordStdin
	t.Cleanup(func() { tuiPasswordStdin = prev })
	tuiPasswordStdin = false
	t.Setenv("RUNWISP_PASSWORD", "test-pw")

	src := resolveTUIAuthSource()
	if !src.remote() {
		t.Fatalf("expected remote path when RUNWISP_PASSWORD is set")
	}
	if src.useStdin {
		t.Fatalf("expected useStdin=false when only env is set")
	}
	if !src.envSet {
		t.Fatalf("expected envSet=true")
	}
}

// TestResolveTUIAuthSource_RemoteOnStdinFlag confirms that --password-stdin
// flips the TUI to the SRP path even without the env var. Stdin wins because
// it's the more explicit signal.
func TestResolveTUIAuthSource_RemoteOnStdinFlag(t *testing.T) {
	prev := tuiPasswordStdin
	t.Cleanup(func() { tuiPasswordStdin = prev })
	tuiPasswordStdin = true
	t.Setenv("RUNWISP_PASSWORD", "")

	src := resolveTUIAuthSource()
	if !src.remote() {
		t.Fatalf("expected remote path when --password-stdin is set")
	}
	if !src.useStdin {
		t.Fatalf("expected useStdin=true")
	}
}

// TestReadRemotePasswordFrom_EnvMissing surfaces an actionable error when the
// operator forgets to export the env var. The message must name the variable
// so the operator knows what to fix.
func TestReadRemotePasswordFrom_EnvMissing(t *testing.T) {
	t.Setenv("RUNWISP_PASSWORD", "")
	_, err := readRemotePasswordFrom("RUNWISP_PASSWORD", "env")
	if err == nil {
		t.Fatal("expected error when env var is unset")
	}
}

// TestReadRemotePasswordFrom_EnvSet returns the env value verbatim.
func TestReadRemotePasswordFrom_EnvSet(t *testing.T) {
	t.Setenv("RUNWISP_PASSWORD", "hunter2")
	got, err := readRemotePasswordFrom("RUNWISP_PASSWORD", "env")
	if err != nil {
		t.Fatal(err)
	}
	if got != "hunter2" {
		t.Fatalf("expected hunter2, got %q", got)
	}
}

// TestReadRemotePasswordFrom_UnsupportedSource fails fast on a typo. Anything
// that isn't "env", "", or "-" is rejected.
func TestReadRemotePasswordFrom_UnsupportedSource(t *testing.T) {
	_, err := readRemotePasswordFrom("RUNWISP_PASSWORD", "file")
	if err == nil {
		t.Fatal("expected error for unsupported source")
	}
}
