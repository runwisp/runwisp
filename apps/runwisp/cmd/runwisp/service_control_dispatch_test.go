// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"strconv"
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
	require.NoError(t, os.WriteFile(datadir.PidFilePath(f.DataDir), []byte(strconv.Itoa(os.Getpid())), 0o600))

	prev := lookupProcessName
	lookupProcessName = func(int) (string, bool) { return "runwisp", true }
	t.Cleanup(func() { lookupProcessName = prev })

	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	return f, buf, cmd
}

// stopTargets and restartTargets drive controlTargets the way runStop and
// runRestart do, against the local socket.
func stopTargets(cmd *cobra.Command, f Flags, args ...string) error {
	return controlTargets(cmd, f, remoteFlags{}, args, "stop", "stopped", (*apiclient.Client).StopTask, (*apiclient.Client).StopRun)
}

func restartTargets(cmd *cobra.Command, f Flags, args ...string) error {
	restart := func(c *apiclient.Client, ctx context.Context, name string) error {
		return c.RestartTask(ctx, name, "cli")
	}
	return controlTargets(cmd, f, remoteFlags{}, args, "restart", "restarted", restart, nil)
}

// tasksHandler serves a fixed /api/tasks list plus a best-effort health check,
// so resolveTargets can classify each target's kind/manualTrigger.
func tasksHandler(itemsJSON string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[` + itemsJSON + `]}`))
	}
}

func TestControlTargets_NoDaemonRunning(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{}
	cmd.SetContext(t.Context())
	cmd.SetOut(&bytes.Buffer{})
	f := Flags{DataDir: testutil.ShortTempDir(t)}

	err := stopTargets(cmd, f, "web")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no daemon is running")
}

func TestControlTargets_StopHappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/api/tasks", tasksHandler(`{"name":"web","kind":"service","manualTrigger":true}`))
	var gotMethod string
	mux.HandleFunc("/api/tasks/web/stop", func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(http.StatusOK)
	})
	f, buf, cmd := serveServiceSocket(t, mux)

	require.NoError(t, stopTargets(cmd, f, "web"))
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Contains(t, buf.String(), `Service "web" stopped.`)
}

func TestControlTargets_TaskStopSaysTask(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/api/tasks", tasksHandler(`{"name":"backup","kind":"task","manualTrigger":true}`))
	mux.HandleFunc("/api/tasks/backup/stop", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	f, buf, cmd := serveServiceSocket(t, mux)

	require.NoError(t, stopTargets(cmd, f, "backup"))
	assert.Contains(t, buf.String(), `Task "backup" stopped.`)
}

// An unknown name alongside a valid one fails before anything is dispatched,
// so a typo in a multi-target command acts on nothing at all.
func TestControlTargets_UnknownNameFailsBeforeDispatch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/api/tasks", tasksHandler(`{"name":"web","kind":"service","manualTrigger":true}`))
	restarted := 0
	mux.HandleFunc("/api/tasks/web/restart", func(w http.ResponseWriter, _ *http.Request) {
		restarted++
		w.WriteHeader(http.StatusNoContent)
	})
	f, _, cmd := serveServiceSocket(t, mux)

	err := restartTargets(cmd, f, "web", "wbe")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"wbe"`)
	assert.Contains(t, err.Error(), `Did you mean "web"?`)
	assert.Zero(t, restarted)
}

func TestControlTargets_ManualTriggerDisabledMaps403(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/api/tasks", tasksHandler(`{"name":"locked","kind":"task","manualTrigger":false}`))
	mux.HandleFunc("/api/tasks/locked/restart", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	f, _, cmd := serveServiceSocket(t, mux)

	err := restartTargets(cmd, f, "locked")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "manual_trigger = false")
}

func TestControlTargets_RunIDHitsStopRunEndpoint(t *testing.T) {
	const runID = "01J8Z3K9QK6VN8XG2R5F7T1C4M"
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/api/tasks", tasksHandler(``))
	var gotPath string
	mux.HandleFunc("/api/runs/"+runID+"/stop", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	})
	f, buf, cmd := serveServiceSocket(t, mux)

	require.NoError(t, stopTargets(cmd, f, runID))
	assert.Equal(t, "/api/runs/"+runID+"/stop", gotPath)
	assert.Contains(t, buf.String(), "Run "+runID+" stopped.")
}

func TestControlTargets_MultiTargetPartialFailureAggregates(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/api/tasks", tasksHandler(
		`{"name":"web","kind":"service","manualTrigger":true},`+
			`{"name":"ghost","kind":"service","manualTrigger":true}`,
	))
	mux.HandleFunc("/api/tasks/web/stop", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/tasks/ghost/stop", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	f, buf, cmd := serveServiceSocket(t, mux)

	err := stopTargets(cmd, f, "web", "ghost")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `stop "ghost"`)
	assert.NotContains(t, err.Error(), `"web"`)
	assert.Contains(t, buf.String(), `Service "web" stopped.`)
}
