// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// publicRoutes is every route that answers a TCP caller with no session
// without a 401 from the JWT gate, keyed "METHOD /chi/pattern", with the status
// that caller gets. Adding a route outside the protected group means adding it
// here, deliberately, with a comment saying what guards it instead.
var publicRoutes = map[string]int{
	// Liveness probe; returns a constant body.
	"GET /health": http.StatusOK,
	// OpenMetrics scrape, only mounted when metrics_enabled is set and no
	// metrics_listen moves it to its own listener. Unauthenticated by
	// Prometheus convention.
	"GET /metrics": http.StatusOK,
	// The UI asks this before it knows whether the caller is logged in.
	"GET /api/auth/status": http.StatusOK,
	// CHAP login flow, rate limited per IP.
	"GET /api/auth/challenge": http.StatusOK,
	"POST /api/auth/login":    http.StatusBadRequest, // no body
	// Launch-ticket redeem: the single-use ticket is the credential.
	"GET /api/auth/launch-ticket": http.StatusUnprocessableEntity, // no ticket
	// Port-conflict discovery for a password-less launcher; the handler refuses
	// anything that is not the socket or a direct loopback peer.
	"GET /api/daemon/identity": http.StatusForbidden,
	// Hooks bypass the session gate but check the unit's hook_tokens
	// themselves, answering 401 to a missing or wrong token.
	"POST /api/hooks/tasks/{taskName}/run":     http.StatusUnauthorized,
	"POST /api/hooks/tasks/{taskName}/start":   http.StatusUnauthorized,
	"POST /api/hooks/tasks/{taskName}/restart": http.StatusUnauthorized,
	"POST /api/hooks/tasks/{taskName}/stop":    http.StatusUnauthorized,
	// Embedded static UI assets and the SPA fallback.
	"GET /*": http.StatusOK,
}

var routeParam = regexp.MustCompile(`\{[^}]+\}`)

// TestRouteAuthCensus walks every route on the chi router and calls it over
// TCP (httptest's default non-loopback RemoteAddr, no local-socket flag) with
// no session. Anything not in publicRoutes must hit the JWT gate's 401, so a
// new route registered outside the protected group fails here until someone
// adds it to the allowlist on purpose.
func TestRouteAuthCensus(t *testing.T) {
	s, repo, _, _ := setupServerWithOpts(t, func(o *Options) { o.MetricsEnabled = true })
	repo.On("GetRunSummary", mock.Anything).Return(&model.RunSummary{}, nil)

	call := func(method, route, authHeader string) int {
		path := strings.ReplaceAll(routeParam.ReplaceAllString(route, "x"), "*", "x")
		req := httptest.NewRequest(method, path, nil)
		if authHeader != "" {
			req.Header.Set("Authorization", authHeader)
		}
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)
		return w.Code
	}

	walked := map[string]bool{}
	require.NoError(t, chi.Walk(s.router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		key := method + " " + route
		walked[key] = true
		want, public := publicRoutes[key]
		if !public {
			want = http.StatusUnauthorized
		}
		assert.Equal(t, want, call(method, route, ""), "%s without a session", key)
		if strings.HasPrefix(route, "/api/hooks/") {
			assert.Equal(t, http.StatusUnauthorized, call(method, route, "Bearer wrong"), "%s with a bad hook token", key)
		}
		return nil
	}))

	for key := range publicRoutes {
		assert.True(t, walked[key], "allowlisted route %s is no longer registered; drop it from publicRoutes", key)
	}

	// Every huma operation must be on the walked router, so one mounted on a
	// mux this test cannot see does not dodge the census.
	for path, item := range s.api.OpenAPI().Paths {
		for method, op := range map[string]*huma.Operation{
			http.MethodGet: item.Get, http.MethodPost: item.Post, http.MethodPut: item.Put,
			http.MethodPatch: item.Patch, http.MethodDelete: item.Delete,
		} {
			if op != nil && !walked[method+" "+path] {
				t.Errorf("huma operation %s %s is not on the chi router", method, path)
			}
		}
	}
}
