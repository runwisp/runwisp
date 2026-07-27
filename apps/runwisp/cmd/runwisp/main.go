// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"os"
	"runtime"
	rdebug "runtime/debug"

	"github.com/charmbracelet/fang"
	"github.com/runwisp/runwisp/internal/version"

	// Embed the IANA time zone database so [scheduler] timezone and per-task
	// timezone resolve on slim images (Alpine/distroless) without an installed
	// tzdata package — one binary, zero runtime deps. Costs ~450 KB.
	_ "time/tzdata"
)

// defaultMemoryLimitBytes caps the Go heap so the daemon trades CPU for RSS
// at steady state rather than growing to 2x live-heap. Applied only when the
// user has not set GOMEMLIMIT explicitly. 128 MiB is comfortably above the
// working set observed for realistic task loads.
const defaultMemoryLimitBytes int64 = 128 << 20

// defaultMaxProcs caps the scheduler's P count, and with it the per-P mcache
// and idle OS-thread pool that scale with GOMAXPROCS. RunWisp is a mostly-idle
// supervisor, not a compute engine, so a handful of Ps is plenty and the
// smaller runtime footprint lowers idle RSS on many-core hosts. No-op on the
// ≤4-core Pi/VPS/container targets.
const defaultMaxProcs = 4

func applyDefaultMemoryLimit() {
	if _, set := os.LookupEnv("GOMEMLIMIT"); set {
		return
	}
	rdebug.SetMemoryLimit(defaultMemoryLimitBytes)
}

// applyDefaultMaxProcs lowers GOMAXPROCS to defaultMaxProcs on big hosts.
// Applied only when GOMAXPROCS is unset, and it only ever lowers the value:
// runtime.GOMAXPROCS(0) reports the current value, which on Go 1.25 already
// reflects a cgroup CPU-quota limit, so a container limited to fewer CPUs keeps
// its smaller value. Pinning it disables the runtime's *dynamic* cgroup
// re-adjustment — an acceptable trade for a long-lived daemon.
func applyDefaultMaxProcs() {
	if _, set := os.LookupEnv("GOMAXPROCS"); set {
		return
	}
	if runtime.GOMAXPROCS(0) > defaultMaxProcs {
		runtime.GOMAXPROCS(defaultMaxProcs)
	}
}

func main() {
	applyDefaultMemoryLimit()
	applyDefaultMaxProcs()
	// fang styles all help/usage pages in the RunWisp brand palette
	// (brandColorScheme). handleCLIError owns *all* error rendering — fang's
	// default error handler is never used — so generic cobra errors and
	// RunWisp's rich userFacingError guidance share one branded look. Signal
	// handling is intentionally left off (no WithNotifySignal) — the daemon
	// installs and owns its own SIGINT/SIGTERM/SIGHUP handlers.
	if err := fang.Execute(
		context.Background(),
		rootCmd,
		fang.WithVersion(version.Version),
		fang.WithColorSchemeFunc(brandColorScheme),
		fang.WithErrorHandler(handleCLIError),
	); err != nil {
		os.Exit(1)
	}
}
