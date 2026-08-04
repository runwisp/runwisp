// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"testing"
)

func TestResolveCronOptions(t *testing.T) {
	// Explicit --system=false forces per-user and disables detection.
	if o := resolveCronOptions("/etc/crontab", false, true); o.System || o.Detect {
		t.Errorf("explicit --system=false should force per-user with no detect, got %+v", o)
	}
	// Explicit --system forces system, even on a non-system path.
	if o := resolveCronOptions("whatever", true, true); !o.System || o.Detect {
		t.Errorf("explicit --system should force system, got %+v", o)
	}
	// A system path forces system when the flag is unset.
	if o := resolveCronOptions("/etc/crontab", false, false); !o.System || o.Detect {
		t.Errorf("system path should force system, got %+v", o)
	}
	// Otherwise the parser auto-detects.
	if o := resolveCronOptions("-", false, false); o.System || !o.Detect {
		t.Errorf("unset + stdin should enable detect, got %+v", o)
	}
}

func TestResolveImportSourceArg(t *testing.T) {
	src, err := resolveImportSource([]string{"/etc/crontab"}, os.Stdin, "crontab")
	if err != nil || src != "/etc/crontab" {
		t.Fatalf("got %q, %v", src, err)
	}
}

func TestResolveImportSourcePipedStdin(t *testing.T) {
	// A regular file is not a TTY, so a no-arg call resolves to stdin.
	f, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	src, err := resolveImportSource(nil, f, "crontab")
	if err != nil || src != "-" {
		t.Fatalf("got %q, %v", src, err)
	}
}
