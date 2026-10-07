// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/runwisp/runwisp/internal/server/auth"
	"github.com/stretchr/testify/assert"
)

func csrfReq(method string, cookie bool, headers map[string]string) *http.Request {
	r := httptest.NewRequest(method, "http://localhost:9477/api/tasks/x/run", nil)
	r.Host = "localhost:9477"
	if cookie {
		r.AddCookie(&http.Cookie{Name: auth.CookieName, Value: "tok"})
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestCSRFGuard(t *testing.T) {
	cases := []struct {
		name       string
		req        *http.Request
		wantpassed bool
	}{
		{"safe GET passes", csrfReq(http.MethodGet, true, nil), true},
		{"no cookie passes", csrfReq(http.MethodPost, false, nil), true},
		{"no cookie same-origin passes", csrfReq(http.MethodPost, false, map[string]string{"Origin": "http://localhost:9477"}), true},
		{"no cookie https origin via untrusted TLS proxy passes", csrfReq(http.MethodPost, false, map[string]string{"Origin": "https://localhost:9477"}), true},
		{"no cookie cross-origin blocked", csrfReq(http.MethodPost, false, map[string]string{"Origin": "https://evil.example"}), false},
		{"no cookie opaque origin blocked", csrfReq(http.MethodPost, false, map[string]string{"Origin": "null"}), false},
		{"no cookie sec-fetch cross-site blocked", csrfReq(http.MethodPost, false, map[string]string{"Sec-Fetch-Site": "cross-site"}), false},
		{"no cookie sec-fetch same-origin passes", csrfReq(http.MethodPost, false, map[string]string{"Sec-Fetch-Site": "same-origin"}), true},
		{"bearer cross-origin blocked without auth mode", csrfReq(http.MethodPost, false, map[string]string{"Authorization": "Bearer t", "Origin": "https://evil.example"}), false},
		{"headless bearer passes", csrfReq(http.MethodPost, false, map[string]string{"Authorization": "Bearer t"}), true},
		{"cookie same-origin passes", csrfReq(http.MethodPost, true, map[string]string{"Origin": "http://localhost:9477"}), true},
		{"cookie https origin via untrusted TLS proxy passes", csrfReq(http.MethodPost, true, map[string]string{"Origin": "https://localhost:9477"}), true},
		{"cookie non-web origin scheme blocked", csrfReq(http.MethodPost, true, map[string]string{"Origin": "ftp://localhost:9477"}), false},
		{"cookie cross-origin blocked", csrfReq(http.MethodPost, true, map[string]string{"Origin": "http://evil.localhost:6006"}), false},
		{"cookie no origin blocked", csrfReq(http.MethodPost, true, nil), false},
		{"cookie referer same-origin passes", csrfReq(http.MethodPost, true, map[string]string{"Referer": "http://localhost:9477/tasks"}), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			passed := false
			h := csrfGuard(false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { passed = true }))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, tc.req)
			assert.Equal(t, tc.wantpassed, passed)
			if !tc.wantpassed {
				assert.Equal(t, http.StatusForbidden, rec.Code)
			}
		})
	}

	// With JWT auth enabled, an explicit Bearer credential is not ambient
	// browser state, so it may use the API cross-origin. The protected route's
	// JWT middleware still verifies the token after this guard.
	bearerReq := csrfReq(http.MethodPost, true, map[string]string{
		"Authorization": "Bearer token",
		"Origin":        "https://evil.example",
	})
	passed := false
	h := csrfGuard(true)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { passed = true }))
	h.ServeHTTP(httptest.NewRecorder(), bearerReq)
	assert.True(t, passed, "explicit Bearer authentication should not depend on an auth cookie's origin headers")

	// A trusted TLS-terminating proxy makes the externally visible request
	// HTTPS even though the daemon's internal connection is plain HTTP.
	req := csrfReq(http.MethodPost, true, map[string]string{"Origin": "https://localhost:9477"})
	req = req.WithContext(context.WithValue(req.Context(), secureContextKey, true))
	passed = false
	h = csrfGuard(false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { passed = true }))
	h.ServeHTTP(httptest.NewRecorder(), req)
	assert.True(t, passed, "trusted HTTPS proxy request should match its public origin")

	// A plaintext page on the same host must not drive a daemon reached over
	// HTTPS (e.g. script a network attacker injected into http://host).
	req = csrfReq(http.MethodPost, true, map[string]string{"Origin": "http://localhost:9477"})
	req = req.WithContext(context.WithValue(req.Context(), secureContextKey, true))
	passed = false
	h.ServeHTTP(httptest.NewRecorder(), req)
	assert.False(t, passed, "http origin must not pass for a secure request")
}
