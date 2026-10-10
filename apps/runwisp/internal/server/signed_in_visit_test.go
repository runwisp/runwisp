// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/apps/runwisp/internal/chap"
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

// signIn runs the CHAP login against host rw.lan:9477 with password, letting
// mutate add headers to the login request, and returns its status.
func signIn(t *testing.T, s *Server, password string, mutate func(*http.Request)) int {
	t.Helper()
	nonce := chapChallenge(t, s)
	body := fmt.Sprintf(`{"nonce":%q,"response":%q}`, nonce, chap.Response(password, nonce))
	req := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "rw.lan:9477"
	if mutate != nil {
		mutate(req)
	}
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	return w.Code
}

func fromBrowser(r *http.Request) { r.Header.Set("Origin", "http://"+r.Host) }

// TestSignedInVisit_ReportsBaseURL covers how the base URL is derived: the
// Host header for a direct sign-in, and the forwarded scheme and host only
// when the peer is a trusted proxy.
func TestSignedInVisit_ReportsBaseURL(t *testing.T) {
	forwarded := func(r *http.Request) {
		fromBrowser(r)
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-Host", "runwisp.example.com, inner.lan")
	}

	t.Run("direct", func(t *testing.T) {
		s, seen := visitServer(t, nil)
		require.Equal(t, http.StatusOK, signIn(t, s, "secret", fromBrowser))
		assert.Equal(t, []string{"http://rw.lan:9477"}, *seen)
	})
	t.Run("trusted proxy", func(t *testing.T) {
		s, seen := visitServer(t, func(o *Options) { o.TrustedProxies = []string{"192.0.2.1"} })
		require.Equal(t, http.StatusOK, signIn(t, s, "secret", forwarded))
		assert.Equal(t, []string{"https://runwisp.example.com"}, *seen)
	})
	t.Run("untrusted peer cannot forge forwarded headers", func(t *testing.T) {
		s, seen := visitServer(t, nil)
		require.Equal(t, http.StatusOK, signIn(t, s, "secret", forwarded))
		assert.Equal(t, []string{"http://rw.lan:9477"}, *seen)
	})
	t.Run("launch ticket", func(t *testing.T) {
		s, seen := visitServer(t, nil)
		ticket, err := s.auth.CreateLaunchTicket()
		require.NoError(t, err)
		req := httptest.NewRequest("GET", "/api/auth/launch-ticket?ticket="+ticket, nil)
		req.Host = "rw.lan:9477"
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		require.Equal(t, http.StatusSeeOther, w.Code)
		assert.Equal(t, []string{"http://rw.lan:9477"}, *seen)
	})
}

// TestSignedInVisit_RejectsMalformedHost: the detected address lands in
// notification links, so a trusted proxy relaying a crafted X-Forwarded-Host
// (or a client sending an odd Host) must not get it recorded.
func TestSignedInVisit_RejectsMalformedHost(t *testing.T) {
	s, seen := visitServer(t, func(o *Options) { o.TrustedProxies = []string{"192.0.2.1"} })
	require.Equal(t, http.StatusOK, signIn(t, s, "secret", func(r *http.Request) {
		fromBrowser(r)
		r.Header.Set("X-Forwarded-Host", `evil.example/"><img src=x onerror=alert(1)>`)
	}))
	assert.Empty(t, *seen)
}

// TestSignedInVisit_OnlyWebUISignIn pins that only a successful browser
// sign-in moves the notification link base: not a failed login, not the CLI
// or remote TUI logging in, and not ordinary authenticated API traffic.
func TestSignedInVisit_OnlyWebUISignIn(t *testing.T) {
	t.Run("wrong password", func(t *testing.T) {
		s, seen := visitServer(t, nil)
		require.Equal(t, http.StatusUnauthorized, signIn(t, s, "wrong", fromBrowser))
		assert.Empty(t, *seen)
	})
	t.Run("CLI login without Origin", func(t *testing.T) {
		s, seen := visitServer(t, nil)
		require.Equal(t, http.StatusOK, signIn(t, s, "secret", nil))
		assert.Empty(t, *seen)
	})
	t.Run("authenticated API request", func(t *testing.T) {
		s, seen := visitServer(t, nil)
		req := httptest.NewRequest("GET", "/api/daemon", nil)
		req.Host = "localhost:9477"
		addAuth(req, s)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, *seen)
	})
}

func TestNormalizeVisitHost(t *testing.T) {
	for in, want := range map[string]string{
		"rw.lan:9477":       "rw.lan:9477",
		"RunWisp.Example":   "runwisp.example",
		"192.168.1.50:9477": "192.168.1.50:9477",
		"[::1]:9477":        "[::1]:9477",
		"[2001:DB8::1]":     "[2001:db8::1]",
		"my_host":           "my_host",
	} {
		got, ok := normalizeVisitHost(in)
		assert.True(t, ok, in)
		assert.Equal(t, want, got, in)
	}
	for _, in := range []string{
		"",
		"::1",
		"[::1",
		"[evil]",
		"[fe80::1%25eth0]",
		"rw.lan:",
		"rw.lan:0",
		"rw.lan:99999",
		"rw.lan:9477/path",
		"user@rw.lan",
		"rw.lan?x=1",
		"rw.lan#frag",
		`rw.lan"`,
		"a&b.lan",
		"a'b.lan",
		"a;b.lan",
		"a b.lan",
		"<rw.lan>",
	} {
		_, ok := normalizeVisitHost(in)
		assert.False(t, ok, in)
	}
}
