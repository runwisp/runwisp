// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cronprobe

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSystemctl stages an executable that prints body and exits with code, so the
// stdout parse can be tested without a systemd on the machine running the suite.
func fakeSystemctl(t *testing.T, body string, code int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "systemctl")
	// %b, not %s: the body carries \n escapes and printf only expands them in %b.
	script := "#!/bin/sh\nprintf '%b' " + strconv.Quote(body) + "\nexit " + strconv.Itoa(code) + "\n"
	require.NoError(t, os.WriteFile(path, []byte(script), 0o700))
	return path
}

// TestAnyStateIs is the guard on asking about every cron unit in one exec. The
// exit code cannot be the answer: `systemctl is-active a b c` exits 0 only when
// *every* unit is active, and a real box has one cron implementation installed and
// two units that do not exist. Reading the exit code would report cron dead on
// every machine cron is actually running on.
func TestAnyStateIs(t *testing.T) {
	units := []string{"cron.service", "crond.service", "cronie.service"}

	t.Run("one active unit among missing ones, despite a non-zero exit", func(t *testing.T) {
		sc := fakeSystemctl(t, "active\ninactive\ninactive\n", 3)
		assert.True(t, anyStateIs(sc, "is-active", units, "active"))
	})

	t.Run("the match may be any line, not just the first", func(t *testing.T) {
		sc := fakeSystemctl(t, "inactive\ninactive\nactive\n", 3)
		assert.True(t, anyStateIs(sc, "is-active", units, "active"))
	})

	t.Run("nothing active is not live", func(t *testing.T) {
		sc := fakeSystemctl(t, "inactive\ninactive\ninactive\n", 3)
		assert.False(t, anyStateIs(sc, "is-active", units, "active"))
	})

	// systemd answers `is-enabled` for a unit it has never heard of with
	// "unknown", and older versions print nothing at all for it.
	t.Run("unknown units are not enabled", func(t *testing.T) {
		sc := fakeSystemctl(t, "unknown\nunknown\nunknown\n", 1)
		assert.False(t, anyStateIs(sc, "is-enabled", units, "enabled"))
	})

	t.Run("a disabled unit is not an enabled one", func(t *testing.T) {
		sc := fakeSystemctl(t, "disabled\ndisabled\ndisabled\n", 1)
		assert.False(t, anyStateIs(sc, "is-enabled", units, "enabled"))
	})

	t.Run("empty output is not live", func(t *testing.T) {
		assert.False(t, anyStateIs(fakeSystemctl(t, "", 1), "is-active", units, "active"))
	})

	t.Run("a systemctl that cannot run is not live", func(t *testing.T) {
		assert.False(t, anyStateIs("/nonexistent/systemctl", "is-active", units, "active"))
	})
}

// TestProbe covers the precedence between the two ways of asking. The init system
// wins whenever it can answer at all, because a stopped-but-enabled cron carries
// the same double-fire risk as a running one with nothing in a pidfile to find.
func TestProbe(t *testing.T) {
	stubServiceProbe := func(t *testing.T, active, enabled, ok bool) {
		t.Helper()
		prev := ServiceProbe
		ServiceProbe = func() (bool, bool, bool) { return active, enabled, ok }
		t.Cleanup(func() { ServiceProbe = prev })
	}
	stubPidFiles := func(t *testing.T, paths ...string) {
		t.Helper()
		prev := PidFiles
		PidFiles = paths
		t.Cleanup(func() { PidFiles = prev })
	}

	dir := t.TempDir()
	livePid := filepath.Join(dir, "live.pid")
	require.NoError(t, os.WriteFile(livePid, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600))
	deadPid := filepath.Join(dir, "dead.pid")
	require.NoError(t, os.WriteFile(deadPid, []byte("999999999\n"), 0o600))
	junkPid := filepath.Join(dir, "junk.pid")
	require.NoError(t, os.WriteFile(junkPid, []byte("not a pid\n"), 0o600))

	t.Run("active", func(t *testing.T) {
		stubServiceProbe(t, true, false, true)
		got := Probe()
		assert.True(t, got.Live)
		assert.Contains(t, got.State, "is running")
	})

	t.Run("enabled but stopped is still live", func(t *testing.T) {
		stubServiceProbe(t, false, true, true)
		got := Probe()
		assert.True(t, got.Live)
		assert.Contains(t, got.State, "next boot")
	})

	t.Run("the init system is the authority when it can answer", func(t *testing.T) {
		stubServiceProbe(t, false, false, true)
		stubPidFiles(t, livePid)
		assert.Equal(t, State{}, Probe(), "a live pidfile must not override a definite no")
	})

	t.Run("no systemctl falls back to a live pidfile", func(t *testing.T) {
		stubServiceProbe(t, false, false, false)
		stubPidFiles(t, deadPid, junkPid, livePid)
		got := Probe()
		assert.True(t, got.Live)
		assert.Contains(t, got.State, "live pidfile")
	})

	// Checking existence alone made a crond that crashed without cleaning up its
	// pidfile — or a stale file baked into an image — look live forever.
	t.Run("a stale pidfile is not a live daemon", func(t *testing.T) {
		stubServiceProbe(t, false, false, false)
		stubPidFiles(t, deadPid, junkPid, filepath.Join(dir, "missing.pid"))
		assert.Equal(t, State{}, Probe())
	})
}

// TestProcessAlive pins the pid guard. pid 0 means "every process in my group" to
// kill(2), so passing it through would report a live crond on any box with a
// zeroed or truncated pidfile.
func TestProcessAlive(t *testing.T) {
	assert.True(t, processAlive(os.Getpid()))
	assert.False(t, processAlive(0))
	assert.False(t, processAlive(-1))
	assert.False(t, processAlive(999999999))
}
