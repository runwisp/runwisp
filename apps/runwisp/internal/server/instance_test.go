// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newServerForInstanceTest(t *testing.T) *Server {
	t.Helper()
	s, _, _, _ := setupServerWithOpts(t, func(o *Options) {
		o.DataDir = "/tmp/rw-data"
		o.ConfigPath = "/tmp/rw-data/runwisp.toml"
		o.SocketPath = "/tmp/rw-data/runwisp.sock"
		o.DaemonInfo = &model.DaemonInfo{Fingerprint: "fp-test"}
	})
	return s
}

func TestInstance_SocketReturnsIdentity(t *testing.T) {
	s := newServerForInstanceTest(t)

	req := httptest.NewRequest("GET", "/api/instance", nil)
	req = req.WithContext(context.WithValue(req.Context(), localTrustedKey{}, true))
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body model.InstanceInfo
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, AppName, body.App)
	assert.Equal(t, "/tmp/rw-data", body.DataDir)
	assert.Equal(t, "/tmp/rw-data/runwisp.toml", body.ConfigPath)
	assert.Equal(t, "/tmp/rw-data/runwisp.sock", body.SocketPath)
	assert.Equal(t, "fp-test", body.Fingerprint)
	assert.NotZero(t, body.Pid)
}

func TestInstance_LoopbackTCPReturnsIdentity(t *testing.T) {
	s := newServerForInstanceTest(t)

	// No local-trusted flag and no JWT: a plain loopback TCP caller. The
	// endpoint is public, so the loopback gate (not the auth middleware) is
	// what admits it — this is exactly the launcher's path on a port conflict.
	req := httptest.NewRequest("GET", "/api/instance", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var body model.InstanceInfo
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	assert.Equal(t, "/tmp/rw-data", body.DataDir)
}

func TestInstance_NonLoopbackTCPReturns403(t *testing.T) {
	s := newServerForInstanceTest(t)

	req := httptest.NewRequest("GET", "/api/instance", nil)
	req.RemoteAddr = "192.168.1.100:54321"
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code,
		"a non-loopback TCP caller must not learn the daemon's datadir/config paths")
	assert.NotContains(t, w.Body.String(), "/tmp/rw-data",
		"filesystem paths must never appear in the 403 body")
}
