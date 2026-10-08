//go:build !windows

// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/apphost"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// appConn is a fake app's connection to the daemon's socket.
type appConn struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

func dialApp(t *testing.T, dataDir string) *appConn {
	t.Helper()
	conn, err := net.Dial("unix", filepath.Join(dataDir, "runwisp.sock"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	_, err = conn.Write([]byte("GET /api/local/app HTTP/1.1\r\nHost: runwisp\r\nConnection: Upgrade\r\nUpgrade: " + apphost.Protocol + "\r\n\r\n"))
	require.NoError(t, err)
	r := bufio.NewReader(conn)
	resp, err := http.ReadResponse(r, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	return &appConn{t: t, conn: conn, r: r}
}

func (a *appConn) send(m map[string]any) {
	a.t.Helper()
	b, err := json.Marshal(m)
	require.NoError(a.t, err)
	_, err = a.conn.Write(append(b, '\n'))
	require.NoError(a.t, err)
}

func (a *appConn) next() map[string]any {
	a.t.Helper()
	require.NoError(a.t, a.conn.SetReadDeadline(time.Now().Add(10*time.Second)))
	line, err := a.r.ReadBytes('\n')
	require.NoError(a.t, err)
	var m map[string]any
	require.NoError(a.t, json.Unmarshal(line, &m))
	return m
}

// An app-supplied config end to end: the daemon boots from the document on
// stdin, the app replaces it live, serves a run whose output and exit
// code land on the run like a shell run's, and the daemon exits once the app
// is gone.
func TestAppDaemon(t *testing.T) {
	t.Parallel()
	projectDir := runwispProjectDir(t)
	binaryPath := buildRunwispBinary(t, projectDir)

	configPath := filepath.Join(t.TempDir(), "runwisp.toml") // anchors paths; never read
	doc := []byte(`{"daemon":{"shutdown_timeout":"500ms"},"tasks":{"boot":{"sdk":true}}}`)
	daemon := launchDaemon(t, projectDir, binaryPath, configPath, testutil.ShortTempDir(t), reserveTCPPort(t), doc)
	client := socketClient(t, daemon.dataDir)
	require.Equal(t, []string{"boot"}, taskNames(t, client))

	app := dialApp(t, daemon.dataDir)
	assert.Equal(t, map[string]any{"type": "role", "active": true}, app.next())

	app.send(map[string]any{"type": "config", "doc": map[string]any{
		"daemon": map[string]any{"shutdown_timeout": "500ms"},
		"tasks":  map[string]any{"digest": map[string]any{"sdk": true, "env": map[string]any{"REGION": "eu"}}},
	}})
	reply := app.next()
	require.Equal(t, "config_ok", reply["type"], "%v", reply)
	require.Equal(t, []string{"digest"}, taskNames(t, client))

	run, err := client.TriggerRun(t.Context(), "digest", nil, "cli")
	require.NoError(t, err)
	msg := app.next()
	assert.Equal(t, "run", msg["type"])
	assert.Equal(t, run.ID, msg["run"])
	assert.Equal(t, "digest", msg["task"])
	assert.Equal(t, map[string]any{"REGION": "eu"}, msg["env"])

	app.send(map[string]any{"type": "output", "run": run.ID, "stream": "stdout", "line": "sent 3 emails"})
	app.send(map[string]any{"type": "exit", "run": run.ID, "code": 3})

	var ended *model.Run
	require.Eventually(t, func() bool {
		ended, err = client.GetRun(t.Context(), run.ID)
		return err == nil && ended.Status == model.PhaseEnded
	}, 5*time.Second, 50*time.Millisecond)
	assert.Equal(t, 3, ended.ExitCode)
	page, err := client.GetLogPage(t.Context(), run.ID, 0, 100)
	require.NoError(t, err)
	assert.Contains(t, joinLogLines(page.Lines), "sent 3 emails")

	require.NoError(t, app.conn.Close())
	require.True(t, daemon.waitForExit(20*time.Second), "daemon should exit once no app is connected:\n%s", daemon.output.Tail(4000))
}
