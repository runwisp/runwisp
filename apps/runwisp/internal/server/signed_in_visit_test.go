// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// visitServer builds a test server that records every OnSignedInVisit call.
func visitServer(t *testing.T, mutate func(*Options)) (*Server, *[]string) {
	t.Helper()
	var seen []string
	s, _, _, _ := setupServerWithOpts(t, func(o *Options) {
		o.OnSignedInVisit = func(u string) { seen = append(seen, u) }
		if mutate != nil {
			mutate(o)
		}
	})
	return s, &seen
}

func visit(s *Server, mutate func(*http.Request)) {
	req := httptest.NewRequest("GET", "/api/daemon", nil)
	req.Host = "rw.lan:9477"
	if mutate != nil {
		mutate(req)
	}
	s.router.ServeHTTP(httptest.NewRecorder(), req)
}

// TestSignedInVisit_ReportsBaseURL covers how the base URL is derived: the
// Host header for a direct visit, and the forwarded scheme and host only when
// the peer is a trusted proxy.
func TestSignedInVisit_ReportsBaseURL(t *testing.T) {
	forwarded := func(r *http.Request) {
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-Host", "runwisp.example.com, inner.lan")
	}

	t.Run("direct", func(t *testing.T) {
		s, seen := visitServer(t, nil)
		visit(s, func(r *http.Request) { addAuth(r, s) })
		assert.Equal(t, []string{"http://rw.lan:9477"}, *seen)
	})
	t.Run("trusted proxy", func(t *testing.T) {
		s, seen := visitServer(t, func(o *Options) { o.TrustedProxies = []string{"192.0.2.1"} })
		visit(s, func(r *http.Request) { addAuth(r, s); forwarded(r) })
		assert.Equal(t, []string{"https://runwisp.example.com"}, *seen)
	})
	t.Run("untrusted peer cannot forge forwarded headers", func(t *testing.T) {
		s, seen := visitServer(t, nil)
		visit(s, func(r *http.Request) { addAuth(r, s); forwarded(r) })
		assert.Equal(t, []string{"http://rw.lan:9477"}, *seen)
	})
}

// TestSignedInVisit_OnlyAuthenticated pins that nothing but a request with a
// valid session token can move the notification link base.
func TestSignedInVisit_OnlyAuthenticated(t *testing.T) {
	t.Run("no token", func(t *testing.T) {
		s, seen := visitServer(t, nil)
		visit(s, nil)
		assert.Empty(t, *seen)
	})
	t.Run("bad token", func(t *testing.T) {
		s, seen := visitServer(t, nil)
		visit(s, func(r *http.Request) { r.Header.Set("Authorization", "Bearer forged") })
		assert.Empty(t, *seen)
	})
	t.Run("unix socket", func(t *testing.T) {
		s, seen := visitServer(t, nil)
		visit(s, func(r *http.Request) {
			*r = *r.WithContext(context.WithValue(r.Context(), localTrustedKey{}, true))
		})
		assert.Empty(t, *seen)
	})
	t.Run("RUNWISP_AUTH=off", func(t *testing.T) {
		s, seen := visitServer(t, func(o *Options) { o.NoAuth = true })
		visit(s, func(r *http.Request) { addAuth(r, s) })
		assert.Empty(t, *seen)
	})
}
