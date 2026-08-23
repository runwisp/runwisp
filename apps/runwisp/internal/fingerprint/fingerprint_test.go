// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package fingerprint

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGenerateDeterministic(t *testing.T) {
	a := Generate()
	b := Generate()
	if a != b {
		t.Errorf("expected deterministic output, got %q and %q", a, b)
	}
	if a == "" {
		t.Error("expected non-empty fingerprint")
	}
}

// TestGenerateIgnoresExecutablePath verifies the doc comment's contract:
// the fingerprint is a function of machine identity + cwd only, not the
// path of the running binary. It reinvokes this same test binary from a
// second, differently-named copy (same machine, same cwd, different
// os.Executable() result) and checks both report the same fingerprint.
// This guards against reintroducing os.Executable()/os.Hostname() into the
// hash, which breaks `service status`/`service uninstall` after the
// binary is reinstalled at a new path (see internal/autostart).
func TestGenerateIgnoresExecutablePath(t *testing.T) {
	if os.Getenv("RUNWISP_FINGERPRINT_TEST_HELPER") == "1" {
		fmt.Print(Generate())
		return
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	data, err := os.ReadFile(self)
	if err != nil {
		t.Fatalf("read test binary: %v", err)
	}
	altPath := filepath.Join(t.TempDir(), "fingerprint-test-copy")
	if err := os.WriteFile(altPath, data, 0o755); err != nil { //nolint:gosec // deliberate copy of our own test binary
		t.Fatalf("write copy of test binary: %v", err)
	}

	run := func(path string) string {
		cmd := exec.Command(path, "-test.run", "^TestGenerateIgnoresExecutablePath$")
		cmd.Env = append(os.Environ(), "RUNWISP_FINGERPRINT_TEST_HELPER=1")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("run %s: %v", path, err)
		}
		return string(out)
	}

	fromOriginal := run(self)
	fromCopy := run(altPath)
	if fromOriginal != fromCopy {
		t.Errorf("fingerprint depends on executable path: %q (%s) vs %q (%s)", fromOriginal, self, fromCopy, altPath)
	}
}

func TestGenerateFormat(t *testing.T) {
	fp := Generate()
	parts := 0
	for _, c := range fp {
		if c == '-' {
			parts++
		}
	}
	if parts != 1 {
		t.Errorf("expected exactly one hyphen in %q", fp)
	}
}
