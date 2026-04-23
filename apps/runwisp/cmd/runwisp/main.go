// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	rdebug "runtime/debug"

	"log/slog"
)

// defaultMemoryLimitBytes caps the Go heap so the daemon trades CPU for RSS
// at steady state rather than growing to 2x live-heap. Applied only when the
// user has not set GOMEMLIMIT explicitly. 128 MiB is comfortably above the
// working set observed for realistic task loads.
const defaultMemoryLimitBytes int64 = 128 << 20

func applyDefaultMemoryLimit() {
	if _, set := os.LookupEnv("GOMEMLIMIT"); set {
		return
	}
	rdebug.SetMemoryLimit(defaultMemoryLimitBytes)
}

func main() {
	applyDefaultMemoryLimit()
	if err := rootCmd.Execute(); err != nil {
		slog.Error("Fatal error", "err", err)
		os.Exit(1)
	}
}
