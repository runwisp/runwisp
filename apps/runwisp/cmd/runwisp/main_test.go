// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	rdebug "runtime/debug"
	"testing"
)

func TestApplyDefaultMemoryLimit_RespectsExplicitGOMEMLIMIT(t *testing.T) {
	t.Setenv("GOMEMLIMIT", "256MiB")

	// SetMemoryLimit(-1) returns the current limit; capture and restore so
	// other tests aren't affected.
	previous := rdebug.SetMemoryLimit(-1)
	t.Cleanup(func() { rdebug.SetMemoryLimit(previous) })

	applyDefaultMemoryLimit()

	if got := rdebug.SetMemoryLimit(-1); got != previous {
		t.Fatalf("limit changed despite GOMEMLIMIT being set: was %d, now %d", previous, got)
	}
}

func TestApplyDefaultMemoryLimit_SetsDefaultWhenUnset(t *testing.T) {
	// t.Setenv can't unset, so save and restore manually.
	orig, hadOrig := os.LookupEnv("GOMEMLIMIT")
	if err := os.Unsetenv("GOMEMLIMIT"); err != nil {
		t.Fatalf("unset GOMEMLIMIT: %v", err)
	}
	t.Cleanup(func() {
		if hadOrig {
			os.Setenv("GOMEMLIMIT", orig)
		} else {
			os.Unsetenv("GOMEMLIMIT")
		}
	})

	previous := rdebug.SetMemoryLimit(-1)
	t.Cleanup(func() { rdebug.SetMemoryLimit(previous) })

	applyDefaultMemoryLimit()

	if got := rdebug.SetMemoryLimit(-1); got != defaultMemoryLimitBytes {
		t.Fatalf("expected limit %d, got %d", defaultMemoryLimitBytes, got)
	}
}
