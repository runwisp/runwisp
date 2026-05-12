// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/runwisp/runwisp/internal/apiclient"
	"github.com/stretchr/testify/require"
)

// TestRunDefault_HealthProbeIsUnauthenticated locks in the precondition for
// the blank-slate fix: the no-subcommand entry point must be able to detect
// a running daemon before it tries to mint a local JWT, because on a blank
// data dir the JWT secret has not yet been seeded.
//
// If a future change reverts to "mint then probe" — i.e. ever passes a
// bearer token on the /health probe — this test starts failing.
func TestRunDefault_HealthProbeIsUnauthenticated(t *testing.T) {
	var sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/health", r.URL.Path)
		sawAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	probe := apiclient.New(srv.URL, "")
	require.NoError(t, probe.HealthCheck())
	require.Empty(t, sawAuth, "blank-slate probe must not send an Authorization header")
}
