// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"os"
	"testing"
)

// ShortTempDir returns an absolute path to a short-named temp directory,
// registered for cleanup via t.Cleanup. Unlike testing.T.TempDir, this avoids
// embedding the test name in the path — important on macOS where the
// sockaddr_un sun_path limit is 104 bytes and long test names push paths
// like "${dir}/runwisp.sock" past the kernel's bind/connect limit, producing
// "invalid argument" instead of a usable Unix domain socket.
func ShortTempDir(t testing.TB) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "rw-")
	if err != nil {
		t.Fatalf("create short temp dir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Logf("cleanup short temp dir %s: %v", dir, err)
		}
	})
	return dir
}
