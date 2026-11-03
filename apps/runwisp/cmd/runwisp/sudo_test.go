// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/apps/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/apps/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `sudo runwisp takeover` was the hint, and sudo's PATH has no ~/.local/bin.
func TestSudoRunwispFor_NamesTheBinaryWhenSudoCannotFindIt(t *testing.T) {
	assert.Equal(t, "sudo runwisp takeover", sudoRunwispFor("/usr/local/bin/runwisp", "takeover"))
	assert.Equal(t, "sudo runwisp password", sudoRunwispFor("/usr/bin/runwisp", "password"))
	assert.Equal(t, "sudo /home/me/.local/bin/runwisp takeover --dry-run",
		sudoRunwispFor("/home/me/.local/bin/runwisp", "takeover", "--dry-run"))
	assert.Equal(t, "sudo runwisp upgrade", sudoRunwispFor("", "upgrade"))
}

func TestSudoArgv_KeepsArgsAndSettingsButNotSecrets(t *testing.T) {
	argv := sudoArgv("/usr/bin/sudo", "/home/me/.local/bin/runwisp",
		[]string{"takeover", "--yes"},
		[]string{"HOME=/home/me", "RUNWISP_PORT=8080", "RUNWISP_PASSWORD=hunter2", "RUNWISP_STATION_TOKEN=t", "RUNWISP_DATA=/srv/rw"})

	assert.Equal(t, []string{
		"/usr/bin/sudo", "RUNWISP_PORT=8080", "RUNWISP_DATA=/srv/rw",
		"/home/me/.local/bin/runwisp", "takeover", "--yes",
	}, argv)
}

// A root service pointed at ~/.local/bin/runwisp runs whatever its owner puts
// there next. A binary the trust check refuses is swapped for a root-owned copy.
func TestUseRootOwnedBinary_CopiesABinaryOthersCanWrite(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "runwisp")
	require.NoError(t, os.WriteFile(bin, []byte("x"), 0o755))
	require.NoError(t, os.Chmod(bin, 0o775)) // group-writable: untrusted

	opts := autostart.InstallOptions{Binary: bin, System: true}
	useRootOwnedBinary(&opts)

	assert.Equal(t, systemBinaryPath, opts.Binary)
	assert.Equal(t, bin, opts.CopyBinaryFrom)
}

// After `takeover`, `runwisp password` from the operator's shell looked in
// ./.runwisp and said no daemon was running, while the system one was up.
func TestDaemonNotRunningHint_PointsAtTheSystemService(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("describes a non-root shell")
	}
	unit := filepath.Join(t.TempDir(), "runwisp.service")
	prev := systemUnitFile
	t.Cleanup(func() { systemUnitFile = prev })
	systemUnitFile = unit

	t.Chdir(t.TempDir()) // a directory with no local setup
	defaults := Flags{CfgFile: defaultConfigPath(os.Geteuid(), ""), DataDir: defaultDataDir(os.Geteuid(), "")}
	assert.Equal(t, startDaemonHint, daemonNotRunningHint(defaults), "no system install here")

	require.NoError(t, os.WriteFile(unit, nil, 0o644))
	hint := daemonNotRunningHint(defaults)
	assert.True(t, strings.HasPrefix(hint, "RunWisp runs here as the system service"), hint)
	assert.Contains(t, hint, "sudo ")
	assert.Equal(t, startDaemonHint, daemonNotRunningHint(Flags{DataDir: "/srv/rw"}),
		"an explicit data dir was not looking for the system daemon")

	// A local setup here may be what was meant: name it first, the system one second.
	require.NoError(t, os.WriteFile(defaults.CfgFile, nil, 0o644))
	hint = daemonNotRunningHint(defaults)
	assert.True(t, strings.HasPrefix(hint, startDaemonHint), hint)
	assert.Contains(t, hint, "For the system service instead")
}

// Once the probes stopped needing sudo, a non-root `runwisp restart` of its own
// ./.runwisp daemon found the box's system unit running and restarted that one.
func TestOwnsSystemDaemon(t *testing.T) {
	assert.True(t, ownsSystemDaemon(Flags{DataDir: ".runwisp"}, 0), "root's commands are the system daemon's")
	assert.True(t, ownsSystemDaemon(Flags{DataDir: "/var/lib/runwisp"}, 1000), "named the system data dir")
	assert.False(t, ownsSystemDaemon(Flags{DataDir: ".runwisp"}, 1000))
	assert.False(t, ownsSystemDaemon(Flags{DataDir: "/tmp/e2e/data"}, 1000))
}

// After `takeover`, `runwisp list` from the operator's shell said only "open
// runwisp.toml: no such file or directory". It now names the system config.
func TestRunList_PointsAtTheSystemConfig(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("describes a non-root shell")
	}
	unit := filepath.Join(t.TempDir(), "runwisp.service")
	prev := systemUnitFile
	t.Cleanup(func() { systemUnitFile = prev })
	systemUnitFile = unit
	require.NoError(t, os.WriteFile(unit, nil, 0o644))

	t.Chdir(t.TempDir())
	defaults := Flags{CfgFile: defaultConfigPath(os.Geteuid(), ""), DataDir: defaultDataDir(os.Geteuid(), "")}
	err := runList(t.Context(), io.Discard, defaults, remoteFlags{}, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/etc/runwisp/runwisp.toml")
	assert.Contains(t, err.Error(), "sudo ")
}

// A socket another user owns is a daemon that is up; the hint must not send the
// operator off to start a second one.
func TestOtherUserHint(t *testing.T) {
	hint := otherUserHint("deploy", []string{"status", "--data", "storage/runwisp"})
	assert.Contains(t, hint, "belongs to deploy")
	assert.Contains(t, hint, "sudo -u deploy ")
	assert.Contains(t, hint, "status --data storage/runwisp")
	assert.NotContains(t, hint, "runwisp daemon")

	hint = otherUserHint("", []string{"status"})
	assert.Contains(t, hint, "another user")
	assert.NotContains(t, hint, "sudo")
}

func TestDaemonUnreachableHint_PermissionDenied(t *testing.T) {
	denied := fmt.Errorf("request failed: %w", &net.OpError{Op: "dial", Net: "unix", Err: os.NewSyscallError("connect", syscall.EACCES)})
	assert.Contains(t, daemonUnreachableHint(Flags{Socket: "/nonexistent/dir/runwisp.sock"}, denied), "another user")
	assert.Equal(t, startDaemonHint, daemonUnreachableHint(Flags{DataDir: "/srv/rw"}, errors.New("connection refused")))
}

// The hint keys off errors.Is(err, fs.ErrPermission), so the client must keep
// that error in its chain all the way up from the dial.
func TestHealthCheckKeepsPermissionDeniedInTheChain(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can open any socket")
	}
	sock := filepath.Join(testutil.ShortTempDir(t), "runwisp.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	require.NoError(t, os.Chmod(sock, 0))

	err = apiclient.NewUnix(sock).HealthCheck(t.Context())
	require.Error(t, err)
	assert.Contains(t, daemonUnreachableHint(Flags{Socket: sock}, err), "belongs to")
}

// A data dir another user keeps private hides its daemon's PID lock, so the
// lock check alone said "no daemon is running, nothing to reload" and exited 0.
func TestReload_AnotherUsersDataDirSaysWhose(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every directory")
	}
	private := testutil.ShortTempDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(private, "daemon.pid"), nil, 0o600))
	require.NoError(t, os.Chmod(private, 0)) // as storage/runwisp (0700, www-data's) looks to the deploy user
	t.Cleanup(func() { _ = os.Chmod(private, 0o700) })

	var out strings.Builder
	err := reloadRunningDaemon(t.Context(), Flags{DataDir: private}, &out)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot open the data dir")
	var ufe *userFacingError
	require.ErrorAs(t, err, &ufe)
	assert.Contains(t, ufe.details, "belongs to")
	assert.NotContains(t, out.String(), "No daemon is running")

	assert.NoError(t, otherUserDaemonError(Flags{DataDir: t.TempDir()}), "a readable dir is left to the lock check")
}
