// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"runtime"
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

// restoreMaxProcs saves GOMAXPROCS (env + runtime value) and restores both on
// cleanup so these tests can't perturb the rest of the suite.
func restoreMaxProcs(t *testing.T) {
	t.Helper()
	origEnv, hadEnv := os.LookupEnv("GOMAXPROCS")
	origVal := runtime.GOMAXPROCS(0)
	t.Cleanup(func() {
		runtime.GOMAXPROCS(origVal)
		if hadEnv {
			os.Setenv("GOMAXPROCS", origEnv)
		} else {
			os.Unsetenv("GOMAXPROCS")
		}
	})
}

func TestApplyDefaultMaxProcs_RespectsExplicitGOMAXPROCS(t *testing.T) {
	restoreMaxProcs(t)
	t.Setenv("GOMAXPROCS", "8")
	runtime.GOMAXPROCS(8)

	applyDefaultMaxProcs()

	if got := runtime.GOMAXPROCS(0); got != 8 {
		t.Fatalf("GOMAXPROCS changed despite being set explicitly: want 8, got %d", got)
	}
}

func TestApplyDefaultMaxProcs_CapsWhenAboveDefault(t *testing.T) {
	restoreMaxProcs(t)
	os.Unsetenv("GOMAXPROCS")
	runtime.GOMAXPROCS(defaultMaxProcs + 4)

	applyDefaultMaxProcs()

	if got := runtime.GOMAXPROCS(0); got != defaultMaxProcs {
		t.Fatalf("expected cap to %d, got %d", defaultMaxProcs, got)
	}
}

func TestApplyDefaultMaxProcs_LeavesSmallerValue(t *testing.T) {
	restoreMaxProcs(t)
	os.Unsetenv("GOMAXPROCS")
	runtime.GOMAXPROCS(2)

	applyDefaultMaxProcs()

	if got := runtime.GOMAXPROCS(0); got != 2 {
		t.Fatalf("expected unchanged 2 (≤ cap), got %d", got)
	}
}
