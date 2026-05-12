// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/go-chi/jwtauth/v5"
	"github.com/runwisp/runwisp/internal/ui"
	"github.com/runwisp/runwisp/internal/version"
	"github.com/sebest/xff"
)

type contextKey string

// peerAddrContextKey stores the original TCP peer address before
// middleware.RealIP can overwrite r.RemoteAddr with spoofable headers.
const peerAddrContextKey contextKey = "peerAddr"

// maxProtectedBodySize bounds request bodies on authenticated routes.
// All currently-defined endpoints take parameters in the URL path only;
// 1 MiB is a generous ceiling that keeps a misbehaving (or malicious) client
// from streaming gigabytes into a handler that doesn't actually read the body.
const maxProtectedBodySize = 1 << 20

// maxBodySize wraps the request body in http.MaxBytesReader so handlers that
// happen to call io.Copy / json.Decode see EOF after the limit.
func maxBodySize(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}

// savePeerAddr captures the original TCP peer address into context.
// Must be registered before middleware.RealIP so that security-critical
// loopback checks (isLoopbackRequest) use the real connection address
// instead of the potentially-spoofed X-Real-IP / X-Forwarded-For value.
func savePeerAddr(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), peerAddrContextKey, r.RemoteAddr)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func tokenFromCookie(name string) func(*http.Request) string {
	return func(r *http.Request) string {
		c, err := r.Cookie(name)
		if err != nil {
			return ""
		}
		return c.Value
	}
}

// securityHeaders adds standard security headers to every response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		// CSP: allow same-origin scripts/styles (Svelte needs unsafe-inline for styles),
		// data URIs for images, and ws/wss for SSE and WebSocket streams.
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self' 'unsafe-inline'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; "+
				"connect-src 'self' ws: wss:; "+
				"font-src 'self'; "+
				"frame-ancestors 'none'",
		)
		next.ServeHTTP(w, r)
	})
}

func (srv *Server) setupRoutes() {
	requestLogger := &middleware.DefaultLogFormatter{Logger: log.New(srv.logOutput, "", log.LstdFlags)}
	// savePeerAddr MUST be first: captures the raw TCP peer address before
	// any proxy-aware middleware (XFF, RealIP) can overwrite r.RemoteAddr.
	srv.router.Use(savePeerAddr)
	if srv.trustedProxies != nil {
		xffmw, err := xff.New(*srv.trustedProxies)
		if err != nil {
			log.Printf("WARNING: invalid trusted_proxies configuration, XFF middleware disabled: %v", err)
		} else {
			srv.router.Use(xffmw.Handler)
		}
	}
	srv.router.Use(securityHeaders)
	srv.router.Use(middleware.RequestLogger(requestLogger))
	srv.router.Use(middleware.Recoverer)
	srv.router.Use(middleware.RequestID)
	srv.router.Use(middleware.RealIP)

	// Create huma API after all global middleware is registered (chi requirement)
	config := huma.DefaultConfig("RunWisp API", version.Version)
	config.Servers = []*huma.Server{{URL: fmt.Sprintf("http://localhost:%d", srv.port)}}
	srv.api = humachi.New(srv.router, config)

	// Health check (raw chi — trivial, no benefit from huma)
	srv.router.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	// Public auth endpoints (huma — only authStatus; challenge is raw chi below)
	srv.registerAuthRoutes()

	// Rate-limited auth endpoints. The /srp/start endpoint allocates the
	// per-login server state and is the more expensive of the two; we share
	// the same per-IP limiter across both so an attacker can't bypass it by
	// shuttling between routes. The launch endpoint sits on the same bucket
	// since it is also a credential-equivalent flow.
	authLimiter := httprate.LimitByIP(MaxAuthAttempts, AuthRateWindow)
	srv.router.With(authLimiter).Post("/api/auth/srp/start", srv.auth.handleSRPStart)
	srv.router.With(authLimiter).Post("/api/auth/srp/finish", srv.auth.handleSRPFinish)
	srv.router.With(authLimiter).Get("/api/auth/launch", srv.handleLaunchTicket)

	// Protected routes
	srv.router.Group(func(r chi.Router) {
		r.Use(maxBodySize(maxProtectedBodySize))
		r.Use(jwtauth.Verify(
			srv.auth.JWTAuth(),
			jwtauth.TokenFromHeader,
			tokenFromCookie(authCookieName),
		))
		r.Use(jwtauth.Authenticator(srv.auth.JWTAuth()))

		// Huma operations (registered on the chi sub-router group)
		srv.registerProtectedHumaRoutes(r)

		// Raw chi handlers for complex endpoints
		r.Post("/api/auth/launch-ticket", srv.handleCreateLaunchTicket)
	})
	ui.Serve(srv.router)
}
