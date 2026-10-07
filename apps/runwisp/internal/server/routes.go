// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/go-chi/jwtauth/v5"
	"github.com/runwisp/runwisp/internal/server/auth"
	"github.com/runwisp/runwisp/internal/ui"
	"github.com/runwisp/runwisp/internal/update"
	"github.com/runwisp/runwisp/internal/version"
)

type contextKey string

// peerAddrContextKey stores the original TCP peer address before the
// client-IP middleware can overwrite r.RemoteAddr from headers.
const peerAddrContextKey contextKey = "peerAddr"

// proxiedContextKey stores whether the request reached the daemon through a
// proxy, so that loopback-only gates can refuse a request whose loopback peer is
// just a same-host reverse proxy relaying an internet client.
const proxiedContextKey contextKey = "proxied"

// secureContextKey stores whether savePeerAddr judged this request to have
// arrived over a secure channel (direct TLS, or X-Forwarded-Proto: https from
// a trusted proxy — see auth.Service.IsSecureRequest). Huma auth handlers
// read it via isSecureCtx since they receive a context.Context, not a
// *http.Request, and so cannot inspect r.TLS themselves.
const secureContextKey contextKey = "secure"

func isSecureCtx(ctx context.Context) bool {
	v, _ := ctx.Value(secureContextKey).(bool)
	return v
}

// forwardedHeaders are the hop headers whose presence means "someone relayed
// this". Any one of them disqualifies a peer from counting as local.
var forwardedHeaders = []string{"X-Forwarded-For", "X-Forwarded-Host", "X-Real-IP", "Forwarded", "CF-Connecting-IP"}

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

// savePeerAddr captures the original TCP peer address into context, along with
// whether the request was relayed by a proxy. Must be registered before the
// client-IP middleware so that security-critical loopback checks
// (isLocalCtx) use the real connection address instead of the
// potentially-spoofed X-Real-IP / X-Forwarded-For value.
func (srv *Server) savePeerAddr(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), peerAddrContextKey, r.RemoteAddr)
		ctx = context.WithValue(ctx, proxiedContextKey, isProxiedRequest(r, srv.trustedProxies))
		ctx = context.WithValue(ctx, secureContextKey, srv.auth.IsSecureRequest(r))
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
		// CSP: script-src and style-src are intentionally NOT set here. The Svelte
		// dashboard ships a per-page <meta> CSP (hash mode — see svelte.config.js)
		// that pins script-src to 'self' plus the hashes of SvelteKit's own inline
		// bootstrap, so no 'unsafe-inline' for scripts is needed and an injected
		// inline <script> in the page body cannot execute. Setting script-src in
		// this header too would intersect with the meta and break the app (the
		// header can't carry the per-build hashes). This header covers everything
		// else and applies to non-HTML responses (JSON/SSE) that carry no meta.
		// concierge.runwisp.com is the Web UI feedback card's endpoint; the
		// browser only calls it when the operator presses Submit.
		w.Header().Set("Content-Security-Policy",
			"img-src 'self' data:; "+
				"connect-src 'self' ws: wss: "+update.DefaultBaseURL+"; "+
				"font-src 'self'; "+
				"object-src 'none'; "+
				"base-uri 'self'; "+
				"form-action 'self'; "+
				"frame-ancestors 'none'",
		)
		// HSTS only over TLS: sending it on a plain-HTTP response would pin a
		// scheme the daemon isn't serving (and a loopback dev daemon would brick
		// http://localhost in the browser for a year).
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// csrfGuard blocks cross-origin state-changing browser requests. SameSite=Strict
// already stops classic cross-*site* requests, but not cross-*origin* ones from
// the same site — another port on localhost, or a compromised sibling subdomain
// — so a same-site page could otherwise POST /api/tasks/{name}/run using the
// victim's cookie. With RUNWISP_AUTH=off there is no cookie to ride on, but a
// cross-site page can still fire body-less simple requests at the daemon, so a
// request that declares a foreign origin is refused whether or not it carries
// a session cookie.
//
// Browsers always send Origin (or Sec-Fetch-Site) on cross-origin unsafe
// requests, so the guard acts on what the request declares:
//   - a session cookie requires a same-origin Origin/Referer, since the cookie is
//     attached automatically and a missing source is itself suspicious;
//   - without one, a request is refused only when its Origin names another
//     host or Sec-Fetch-Site says cross-site. Headless clients (CLI, curl,
//     apiclient) send neither and keep working under RUNWISP_AUTH=off.
//
// It is inert for safe methods (GET/HEAD/OPTIONS), local-trusted requests on
// the Unix socket (CLI/TUI), and explicit Bearer requests when JWT auth is
// enabled. In auth-off mode, Authorization may be ambient proxy/basic auth, so
// it does not exempt a browser request. Headless clients without browser source
// headers keep working in either mode.
func csrfGuard(allowBearer bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if csrfExempt(r, allowBearer) {
				next.ServeHTTP(w, r)
				return
			}
			// Launch-ticket mint is CSRF-safe on its own: its only effect is
			// returning a short-lived ticket in the response body, which the
			// browser's same-origin policy stops a cross-origin attacker from
			// reading. The remote-TUI/CLI hand-off mints it without a browser Origin.
			if r.URL.Path == "/api/auth/launch-ticket" {
				next.ServeHTTP(w, r)
				return
			}
			if csrfSourceRejected(r) {
				http.Error(w, "cross-origin request refused", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func csrfExempt(r *http.Request, allowBearer bool) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	if IsLocalTrusted(r) {
		return true
	}
	if !allowBearer {
		return false
	}
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	return len(authorization) > len("Bearer ") && strings.EqualFold(authorization[:len("Bearer ")], "Bearer ")
}

func csrfSourceRejected(r *http.Request) bool {
	_, cookieErr := r.Cookie(auth.CookieName)
	if cookieErr == nil {
		return !sameOriginRequest(r)
	}
	return declaredCrossOrigin(r)
}

// sameOriginRequest reports whether the request's Origin (or, as a fallback,
// Referer) names the same host:port the request was sent to. A state-changing
// browser request always carries one of the two; their absence on a
// cookie-authenticated unsafe method is itself suspicious and is refused.
func sameOriginRequest(r *http.Request) bool {
	src := r.Header.Get("Origin")
	if src == "" {
		src = r.Header.Get("Referer")
	}
	if src == "" {
		return false
	}
	return sourceMatchesRequest(src, r)
}

// declaredCrossOrigin reports whether the request itself says it came from
// another origin: Sec-Fetch-Site: cross-site, or an Origin (including the
// opaque "null") that doesn't name the host it was sent to. A request with
// neither header is not a browser cross-origin request.
func declaredCrossOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return true
	}
	origin := r.Header.Get("Origin")
	return origin != "" && !sourceMatchesRequest(origin, r)
}

func sourceMatchesRequest(src string, r *http.Request) bool {
	u, err := url.Parse(src)
	if err != nil || u.Host == "" || u.User != nil || u.Opaque != "" {
		return false
	}
	if !strings.EqualFold(u.Host, r.Host) {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return true
	case "http":
		// A plaintext page on the same host (e.g. script injected by a network
		// attacker) must not drive a daemon that is reached over HTTPS. When the
		// request does not look secure the scheme is unknowable (a TLS proxy
		// outside trusted_proxies looks like plain HTTP), so either one passes.
		return r.TLS == nil && !isSecureCtx(r.Context())
	}
	return false
}

func (srv *Server) setupRoutes() error {
	// savePeerAddr MUST be first: captures the raw TCP peer address before the
	// client-IP middleware can overwrite r.RemoteAddr.
	srv.router.Use(srv.savePeerAddr)
	srv.router.Use(srv.resolveClientIP)
	srv.router.Use(securityHeaders)
	srv.router.Use(middleware.RequestLogger(slogAccessLogger{}))
	srv.router.Use(middleware.Recoverer)
	srv.router.Use(middleware.RequestID)
	// NOTE: we deliberately do NOT use chi's middleware.RealIP. It trusts
	// X-Forwarded-For / X-Real-IP / True-Client-IP from *any* client, which
	// would let a remote attacker rotate those headers to mint a fresh
	// per-IP bucket on every request and bypass the auth rate limiter
	// (httprate.LimitByIP below keys off r.RemoteAddr). resolveClientIP above
	// rewrites r.RemoteAddr from XFF only when the immediate peer is in the
	// operator's trusted-proxy set, and then takes the rightmost hop that is not
	// itself a trusted proxy, so a client cannot choose its own bucket. Do not
	// re-add middleware.RealIP.

	// Create huma API after all global middleware is registered (chi requirement)
	config := huma.DefaultConfig("RunWisp API", version.Version)
	scheme := srv.scheme
	if scheme == "" {
		scheme = "http"
	}
	config.Servers = []*huma.Server{{URL: fmt.Sprintf("%s://localhost:%d", scheme, srv.port)}}
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
		// Unauthenticated, per the Prometheus exposition convention (node_exporter,
		// etcd, cAdvisor et al. all serve /metrics without auth). Operators who
		// need isolation bind a dedicated [daemon] metrics_listen (etcd's
		// --listen-metrics-urls model) or front it with a reverse proxy.
		srv.router.Get("/metrics", srv.handleOpenMetrics)
	}

	// Public auth status endpoint. No rate limit (nothing to flood — it only
	// reads a cookie) and outside the protected group (the UI calls it before
	// it knows whether the caller is authenticated).
	srv.registerAuthRoutes()

	// Public identity endpoint. Outside the authOrLocalTrusted group so a
	// password-less launcher can query it on a port conflict; the handler's own
	// loopback gate keeps its datadir/config/socket paths local-only.
	srv.registerInstanceRoute()

	// Rate-limited auth endpoints: challenge, login, and launch-ticket redeem
	// share one limit to prevent nonce-store flooding (DoS on the auth flow).
	srv.router.Group(func(r chi.Router) {
		r.Use(httprate.LimitByIP(auth.MaxAuthAttempts, auth.AuthRateWindow))
		srv.registerRateLimitedAuthRoutes(r)
	})

	// Hooks: /api/hooks/<session route>, authenticated by the unit's
	// hook_tokens. Outside the protected group: the token is the credential,
	// checked by the handler (also with RUNWISP_AUTH=off). No cookie is read,
	// so no CSRF guard is needed. The limiter counts rejected tokens only,
	// never successful calls.
	srv.router.Group(func(r chi.Router) {
		r.Use(maxBodySize(maxProtectedBodySize))
		r.Use(hookFailureLimiter())
		srv.registerHookRoutes(r)
	})

	// Protected routes. With RUNWISP_AUTH=off the JWT gate is skipped entirely
	// — an explicit operator opt-in, warned about loudly at startup — but the
	// body-size cap stays in place.
	srv.router.Group(func(r chi.Router) {
		r.Use(maxBodySize(maxProtectedBodySize))
		r.Use(csrfGuard(!srv.noAuth))
		if !srv.noAuth {
			r.Use(authOrLocalTrusted(srv.auth))
		}

		// Huma operations (registered on the chi sub-router group)
		srv.registerProtectedHumaRoutes(r)
	})
	return ui.Mount(srv.router)
}
