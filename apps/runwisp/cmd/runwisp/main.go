// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	rdebug "runtime/debug"
	"syscall"

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
	// Restrict perms on every file the daemon creates from now on (data dir,
	// SQLite -wal/-shm sidecars, PID file, logs). 0077 leaves owner full
	// access and denies group/other. The DB main file is also chmod'd
	// explicitly inside storage.New for cases where it predates this umask.
	syscall.Umask(0077)

	applyDefaultMemoryLimit()
	if err := rootCmd.Execute(); err != nil {
		if ufe, ok := isUserFacing(err); ok {
			renderUserFacingError(os.Stderr, ufe)
		} else {
			slog.Error("Fatal error", "err", err)
		}
		os.Exit(1)
	}
}
