// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package server

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/apphost"
	"github.com/runwisp/runwisp/apps/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An app connection runs handler code with the daemon's trust, so it is
// refused over TCP even when RUNWISP_AUTH=off lets every other route through.
func TestAppsRoute_RefusedOverTCPEvenWithAuthOff(t *testing.T) {
	s, _, _, _ := setupServerWithOpts(t, func(o *Options) {
		o.NoAuth = true
		o.Apps = apphost.New()
	})
	req := httptest.NewRequest("GET", "/api/local/app", nil)
	req.Header.Set("Upgrade", apphost.Protocol)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

// The upgrade survives the router's middleware and the socket server's
// timeouts: the app gets 101 and then its role on the same connection.
func TestAppsRoute_UpgradesOnTheSocket(t *testing.T) {
	sockDir := testutil.ShortTempDir(t)
	apps := apphost.New()
	s, _, _, _ := setupServerWithOpts(t, func(o *Options) {
		o.Host = "127.0.0.1"
		o.SocketPath = filepath.Join(sockDir, "runwisp.sock")
		o.Apps = apps
	})
	s.port = testutil.PickFreePort(t)
	errCh := make(chan error, 1)
	go func() { errCh <- s.Start() }()
	t.Cleanup(func() {
		apps.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
		<-errCh
	})
	requireSocketReady(t, s.socketPath, 2*time.Second)

	conn, err := net.Dial("unix", s.socketPath)
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.Write([]byte("GET /api/local/app HTTP/1.1\r\nHost: runwisp\r\nConnection: Upgrade\r\nUpgrade: " + apphost.Protocol + "\r\n\r\n"))
	require.NoError(t, err)

	r := bufio.NewReader(conn)
	resp, err := http.ReadResponse(r, nil)
	require.NoError(t, err)
	assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)

	line, err := r.ReadString('\n')
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"role","active":true}`, line)
}
