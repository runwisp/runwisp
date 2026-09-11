// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/runwisp/runwisp/internal/datadir"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serveServiceSocket binds an HTTP mux to the daemon's Unix socket and makes
// isDaemonRunning see a live daemon: it writes a PID file for this test process
// and points lookupProcessName at a runwisp-named process so processIsDaemon
// accepts it. Returns Flags aimed at the socket plus a command whose stdout is
// captured.
func serveServiceSocket(t *testing.T, mux http.Handler) (Flags, *bytes.Buffer, *cobra.Command) {
	t.Helper()
	f := serveStatusSocket(t, mux)
	require.NoError(t, datadir.WritePidFile(f.DataDir))

	prev := lookupProcessName
	lookupProcessName = func(int) (string, bool) { return "runwisp", true }
	t.Cleanup(func() { lookupProcessName = prev })

	cmd := &cobra.Command{}
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	return f, buf, cmd
}

func TestControlService_NoDaemonRunning(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	f := Flags{DataDir: testutil.ShortTempDir(t)}

	err := controlService(cmd, f, "web", "stop", "stopped", (*apiclient.Client).StopService)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no daemon is running")
}

func TestControlService_StopHappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	var gotMethod string
	mux.HandleFunc("/api/tasks/web/stop", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(http.StatusOK)
	})
	f, buf, cmd := serveServiceSocket(t, mux)

	require.NoError(t, controlService(cmd, f, "web", "stop", "stopped", (*apiclient.Client).StopService))
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Contains(t, buf.String(), `Service "web" stopped.`)
}

func TestControlService_UnknownTaskMaps404(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/api/tasks/ghost/restart", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	// daemonTaskNames' best-effort suggestion fetch.
	mux.HandleFunc("/api/tasks", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"items":[]}`))
	})
	f, _, cmd := serveServiceSocket(t, mux)

	err := controlService(cmd, f, "ghost", "restart", "restarted", (*apiclient.Client).RestartService)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ghost")
}

func TestControlService_NotAServiceMaps400(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/api/tasks/backup/restart", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})
	f, _, cmd := serveServiceSocket(t, mux)

	err := controlService(cmd, f, "backup", "restart", "restarted", (*apiclient.Client).RestartService)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a task, not a service")
}
