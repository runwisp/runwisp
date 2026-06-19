// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/go-chi/jwtauth/v5"
	"github.com/runwisp/runwisp/internal/server/auth"
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

// authOrLocalTrusted short-circuits JWT verification for requests delivered
// on the Unix socket. Filesystem permissions (0600 socket + 0700 datadir)
// and SO_PEERCRED at accept time already gated access; running CHAP/JWT on
// top would add no security and would force the local CLI/TUI to keep a
// password around just to talk to the daemon that owns its data dir.
//
// For TCP requests (no local-trusted flag), the usual jwtauth verifier and
// authenticator chain runs unchanged.
func authOrLocalTrusted(authSvc *auth.Service) func(http.Handler) http.Handler {
	verify := jwtauth.Verify(
		authSvc.JWTAuth(),
		jwtauth.TokenFromHeader,
		auth.TokenFromCookie,
	)
	authenticator := jwtauth.Authenticator(authSvc.JWTAuth())
	return func(next http.Handler) http.Handler {
		fallback := verify(authenticator(next))
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if IsLocalTrusted(r) {
				next.ServeHTTP(w, r)
				return
			}
			fallback.ServeHTTP(w, r)
		})
	}
}

// savePeerAddr captures the original TCP peer address into context.
// Must be registered before middleware.RealIP so that security-critical
// loopback checks (isLocalRequest) use the real connection address
// instead of the potentially-spoofed X-Real-IP / X-Forwarded-For value.
func savePeerAddr(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), peerAddrContextKey, r.RemoteAddr)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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

func (srv *Server) setupRoutes() error {
	// savePeerAddr MUST be first: captures the raw TCP peer address before
	// any proxy-aware middleware (XFF, RealIP) can overwrite r.RemoteAddr.
	srv.router.Use(savePeerAddr)
	if srv.trustedProxies != nil {
		xffmw, err := xff.New(*srv.trustedProxies)
		if err != nil {
			slog.Warn("Invalid trusted_proxies configuration; XFF middleware disabled", "err", err)
		} else {
			srv.router.Use(xffmw.Handler)
		}
	}
	srv.router.Use(securityHeaders)
	srv.router.Use(middleware.RequestLogger(slogAccessLogger{}))
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

	// OpenMetrics scrape endpoint. Off by default; when enabled without a
	// dedicated [daemon] metrics_listen it mounts here, parallel to /health.
	// A non-empty metrics_listen routes scrapes to a separate http.Server
	// stood up in Start(), so we deliberately do NOT mount on the main mux.
	if srv.metricsEnabled && srv.metricsListen == "" {
		srv.router.Get("/metrics", srv.handleOpenMetrics)
	}

	// Public auth endpoints (huma — only authStatus; challenge is raw chi below)
	srv.registerAuthRoutes()

	// Public identity endpoint. Outside the authOrLocalTrusted group so a
	// password-less launcher can query it on a port conflict; the handler's own
	// loopback gate keeps its datadir/config/socket paths local-only.
	srv.registerInstanceRoute()

	// Rate-limited auth endpoints: both challenge and login share the same limit
	// to prevent nonce-store flooding (DoS on the auth flow).
	authLimiter := httprate.LimitByIP(auth.MaxAuthAttempts, auth.AuthRateWindow)
	srv.router.With(authLimiter).Get("/api/auth/challenge", srv.handleAuthChallenge)
	srv.router.With(authLimiter).Post("/api/auth", srv.auth.HandleLogin)
	srv.router.With(authLimiter).Get("/api/auth/launch", srv.handleLaunchTicket)

	// Protected routes. With RUNWISP_NO_AUTH the JWT gate is skipped entirely
	// — an explicit operator opt-in, warned about loudly at startup — but the
	// body-size cap stays in place.
	srv.router.Group(func(r chi.Router) {
		r.Use(maxBodySize(maxProtectedBodySize))
		if !srv.noAuth {
			r.Use(authOrLocalTrusted(srv.auth))
		}

		// Huma operations (registered on the chi sub-router group)
		srv.registerProtectedHumaRoutes(r)

		// Raw chi handlers for complex endpoints
		r.Post("/api/auth/launch-ticket", srv.handleCreateLaunchTicket)
	})
	return ui.Mount(srv.router)
}
