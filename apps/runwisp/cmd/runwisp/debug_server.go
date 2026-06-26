// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"time"

	"log/slog"
)

// DebugAddrEnv names the env var that opts into the pprof debug server, e.g.
// RUNWISP_DEBUG_ADDR=127.0.0.1:6060. Off entirely when unset.
const DebugAddrEnv = "RUNWISP_DEBUG_ADDR"

// debugServer is an opt-in, loopback-only net/http/pprof endpoint, used to
// profile memory and CPU (e.g. `go tool pprof http://127.0.0.1:6060/debug/pprof/heap`).
// It deliberately does NOT ride on the authenticated REST/UI server: profiles
// expose process memory, and that surface is untrusted (see TRUST MODEL). It is
// loopback-only and off by default.
type debugServer struct {
	srv  *http.Server
	addr string // actual bound address (host:port), resolved after Listen
}

// startDebugServer starts the pprof server when RUNWISP_DEBUG_ADDR names a
// loopback address. Returns nil (no server) when the env var is unset — the
// common case. A non-loopback address is refused with a warning rather than
// silently exposing profiles to the network.
func startDebugServer() *debugServer {
	addr, ok := os.LookupEnv(DebugAddrEnv)
	if !ok || addr == "" {
		return nil
	}
	if !isLoopbackAddr(addr) {
		slog.Warn("ignoring "+DebugAddrEnv+": only loopback addresses may serve the pprof endpoint", "addr", addr)
		return nil
	}

	mux := http.NewServeMux()
	// pprof.Index serves /debug/pprof/ and dispatches the named runtime profiles
	// (heap, goroutine, allocs, ...) under it; the others are their own handlers.
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Warn("failed to start pprof debug server", "addr", addr, "err", err)
		return nil
	}

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			slog.Warn("pprof debug server stopped", "err", serveErr)
		}
	}()
	slog.Warn("pprof debug server enabled — exposes process memory; loopback only", "addr", ln.Addr().String())
	return &debugServer{srv: srv, addr: ln.Addr().String()}
}

// Close shuts the debug server down. Safe on a nil receiver (server never
// started), so the shutdown path can call it unconditionally.
func (d *debugServer) Close() {
	if d == nil || d.srv == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := d.srv.Shutdown(ctx); err != nil {
		slog.Debug("pprof debug server shutdown error", "err", err)
	}
}

// isLoopbackAddr reports whether host:port binds only to loopback. An explicit
// 127.0.0.1/::1 or "localhost" qualifies; an empty host (":6060" → all
// interfaces), 0.0.0.0, or any routable IP does not.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
