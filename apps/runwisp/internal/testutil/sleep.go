// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// StubSleep shadows sleep(1) with a no-op on PATH for the rest of the test, so
// shell tasks that pace their output for a human (the demo config) run at full
// speed. Uses t.Setenv, so the test can't be parallel.
func StubSleep(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sleep"), []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // the stub must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
