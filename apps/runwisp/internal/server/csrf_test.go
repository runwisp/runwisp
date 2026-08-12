// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
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
		{"bearer passes", csrfReq(http.MethodPost, true, map[string]string{"Authorization": "Bearer t"}), true},
		{"cookie same-origin passes", csrfReq(http.MethodPost, true, map[string]string{"Origin": "http://localhost:9477"}), true},
		{"cookie cross-origin blocked", csrfReq(http.MethodPost, true, map[string]string{"Origin": "http://evil.localhost:6006"}), false},
		{"cookie no origin blocked", csrfReq(http.MethodPost, true, nil), false},
		{"cookie referer same-origin passes", csrfReq(http.MethodPost, true, map[string]string{"Referer": "http://localhost:9477/tasks"}), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			passed := false
			h := csrfGuard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { passed = true }))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, tc.req)
			assert.Equal(t, tc.wantpassed, passed)
			if !tc.wantpassed {
				assert.Equal(t, http.StatusForbidden, rec.Code)
			}
		})
	}
}
