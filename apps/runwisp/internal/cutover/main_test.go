// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cutover

import (
	"os"
	"syscall"
	"testing"
)

// The cron trust check rejects group-writable directories, so pin the umask or
// t.TempDir() trees fail it on hosts that default to 002.
func TestMain(m *testing.M) {
	syscall.Umask(0o022)
	os.Exit(m.Run())
}
