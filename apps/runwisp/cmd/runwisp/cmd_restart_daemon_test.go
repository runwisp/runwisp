// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/apps/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/testutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// restartInstaller is an autostart.Installer whose only working method is
// Restart; the embedded nil interface panics on anything else.
type restartInstaller struct {
	autostart.Installer
	err error
}

func (r restartInstaller) Restart(context.Context, autostart.InstallOptions) error { return r.err }

func restartTestCmd(t *testing.T) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&bytes.Buffer{})
	return cmd
}

func writePid(t *testing.T, f Flags, pid int) {
	t.Helper()
	require.NoError(t, os.WriteFile(datadir.PidFilePath(f.DataDir), []byte(strconv.Itoa(pid)), 0o600))
}

func TestRestartViaServiceWaitsForHealth(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/api/daemon", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(model.DaemonInfo{})
	})
	f := serveStatusSocket(t, mux)

	var out bytes.Buffer
	st := autostart.Status{OS: "linux", UnitPath: "/x/runwisp.service"}
	require.NoError(t, restartViaService(&out, restartInstaller{}, autostart.InstallOptions{}, st, f))
	assert.Contains(t, out.String(), "systemd")
	assert.Contains(t, out.String(), "Daemon restarted")
}

func TestRestartViaServicePropagatesInstallerError(t *testing.T) {
	boom := errors.New("boom")
	err := restartViaService(&bytes.Buffer{}, restartInstaller{err: boom}, autostart.InstallOptions{}, autostart.Status{OS: "darwin"}, Flags{DataDir: t.TempDir()})
	assert.ErrorIs(t, err, boom)
}

func TestRestartInPlaceNeedsAReachableDaemon(t *testing.T) {
	err := restartInPlace(restartTestCmd(t), Flags{DataDir: testutil.ShortTempDir(t)}, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot reach the daemon")
}

func TestRestartInPlaceNeedsAPidFile(t *testing.T) {
	f := serveStatusSocket(t, daemonInfoMux(&atomic.Int32{}))
	err := restartInPlace(restartTestCmd(t), f, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "PID file")
}

func TestRestartInPlaceReportsAnUnsignalableProcess(t *testing.T) {
	f := serveStatusSocket(t, daemonInfoMux(&atomic.Int32{}))
	writePid(t, f, 1<<22+12345) // above any real pid_max
	err := restartInPlace(restartTestCmd(t), f, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot signal")
}

// A process that dies on the signal and never answers again is a daemon that
// stopped instead of restarting.
func TestRestartInPlaceDetectsADaemonThatStopped(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/daemon", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(model.DaemonInfo{StartedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)})
	})
	f := serveStatusSocket(t, mux)
	cmd := exec.Command("sleep", "30")
	require.NoError(t, cmd.Start())
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	writePid(t, f, cmd.Process.Pid)

	err := restartInPlace(restartTestCmd(t), f, filepath.Join(t.TempDir(), "none.toml"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stopped instead of starting again")
}

// A process that survives the signal and answers with a newer start time is a
// daemon that restarted.
func TestRestartInPlaceSucceedsWhenTheStartTimeAdvances(t *testing.T) {
	f := serveStatusSocket(t, daemonInfoMux(&atomic.Int32{}))
	cmd := exec.Command("sh", "-c", `trap "" USR2; sleep 30`)
	require.NoError(t, cmd.Start())
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	time.Sleep(200 * time.Millisecond) // let sh install the trap
	writePid(t, f, cmd.Process.Pid)

	cmdOut := restartTestCmd(t)
	require.NoError(t, restartInPlace(cmdOut, f, filepath.Join(t.TempDir(), "none.toml")))
	assert.Contains(t, cmdOut.OutOrStdout().(*bytes.Buffer).String(), "Restarting the daemon")
}

// daemonInfoMux answers /api/daemon with a start time that moves on after the
// first call, as a restarted daemon's would.
func daemonInfoMux(calls *atomic.Int32) http.Handler {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/daemon", func(w http.ResponseWriter, _ *http.Request) {
		started := base.Add(time.Duration(calls.Add(1)-1) * time.Hour)
		_ = json.NewEncoder(w).Encode(model.DaemonInfo{StartedAt: started})
	})
	return mux
}

func identityMux(source string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/daemon/identity", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(model.InstanceInfo{ConfigSource: source, ConfigPath: "/etc/rw.toml"})
	})
	return mux
}

func TestRunningDaemonInfoReadsTheDaemonAndRefusesAnApp(t *testing.T) {
	f := serveStatusSocket(t, identityMux("file"))
	info, err := runningDaemonInfo(restartTestCmd(t), f, true)
	require.NoError(t, err)
	assert.Equal(t, "/etc/rw.toml", info.ConfigPath)

	f = serveStatusSocket(t, identityMux("app"))
	_, err = runningDaemonInfo(restartTestCmd(t), f, true)
	assert.Error(t, err)
}

func TestRunningDaemonInfoTreatsASilentDaemonAsUnknown(t *testing.T) {
	info, err := runningDaemonInfo(restartTestCmd(t), Flags{DataDir: testutil.ShortTempDir(t)}, true)
	assert.NoError(t, err)
	assert.Nil(t, info)
}

func TestConnectedAppDaemon(t *testing.T) {
	assert.True(t, connectedAppDaemon(context.Background(), serveStatusSocket(t, identityMux("app"))))
	assert.False(t, connectedAppDaemon(context.Background(), serveStatusSocket(t, identityMux("file"))))
	assert.False(t, connectedAppDaemon(context.Background(), Flags{DataDir: testutil.ShortTempDir(t)}))
}
