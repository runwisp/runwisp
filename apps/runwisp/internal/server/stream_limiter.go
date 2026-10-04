// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"errors"
	"net/http"
	"sync"

	"github.com/danielgtaylor/huma/v2"
)

// Default caps for concurrent SSE / log-stream connections. A browser tab now
// holds a single unified app-event stream (/api/events/stream) plus an on-demand log
// tail, so these caps leave generous headroom for a handful of tabs while still
// bounding resource use under abuse.
const (
	maxConcurrentStreams = 128
	maxStreamsPerIP      = 16
)

// streamLimiter caps the number of concurrent long-lived streaming connections
// (SSE, log streams) so that a single client cannot exhaust the daemon's file
// descriptors and goroutines by holding many streams open.
type streamLimiter struct {
	maxGlobal int
	maxPerIP  int

	mu    sync.Mutex
	total int
	perIP map[string]int
}

func newStreamLimiter(maxGlobal, maxPerIP int) *streamLimiter {
	return &streamLimiter{
		maxGlobal: maxGlobal,
		maxPerIP:  maxPerIP,
		perIP:     make(map[string]int),
	}
}

// acquire reserves a stream slot for the request carried by ctx. The global
// cap always applies; the per-IP sub-cap does not, for connections accepted
// on the local Unix socket (PEERCRED-verified). Those connections have no
// usable peer address — net.UnixConn.RemoteAddr() is empty for an unnamed
// client socket — so every local TUI/CLI stream would otherwise collapse
// into one shared bucket keyed by "", capping all local clients on the
// machine combined at maxPerIP instead of bounding each one individually.
// It returns release (always non-nil when ok) which the caller must invoke
// when the stream finishes.
func (l *streamLimiter) acquire(ctx context.Context) (release func(), ok bool) {
	ip := streamClientIPFromCtx(ctx)
	exempt := IsLocalTrustedCtx(ctx)

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.total >= l.maxGlobal {
		return nil, false
	}
	if !exempt && l.perIP[ip] >= l.maxPerIP {
		return nil, false
	}

	l.total++
	if !exempt {
		l.perIP[ip]++
	}

	return func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.total--
		if exempt {
			return
		}
		if l.perIP[ip] <= 1 {
			delete(l.perIP, ip)
		} else {
			l.perIP[ip]--
		}
	}, true
}

// streamClientIPFromCtx returns the client IP resolved by resolveClientIP: the
// TCP peer, or behind a trusted proxy the rightmost untrusted X-Forwarded-For
// hop, which a client cannot rotate to dodge the cap. It reads from context
// because huma SSE handlers do not receive the *http.Request directly.
func streamClientIPFromCtx(ctx context.Context) string {
	ip, _ := ctx.Value(clientIPKey{}).(string)
	return ip
}

// streamGate is the operation middleware every SSE route installs. huma commits
// the 200 header before the stream callback runs, so a refusal made inside the
// callback can only be an empty 200 body. Admission therefore happens here,
// before the response starts: 503 past the stream cap, then the route's own
// check (if any). The slot is held until the stream ends.
func (srv *Server) streamGate(api huma.API, check func(huma.Context) huma.StatusError) huma.Middlewares {
	return huma.Middlewares{func(hctx huma.Context, next func(huma.Context)) {
		release, ok := srv.streams.acquire(hctx.Context())
		if !ok {
			_ = huma.WriteErr(api, hctx, http.StatusServiceUnavailable, "Too many open streams; close some and retry")
			return
		}
		defer release()
		if check != nil {
			if err := check(hctx); err != nil {
				_ = huma.WriteErr(api, hctx, err.GetStatus(), err.Error())
				return
			}
		}
		next(hctx)
	}}
}

// requireStreamableRun answers 404 for a run ID that matches no run. A
// malformed ID is left to huma's own parameter validation.
func (srv *Server) requireStreamableRun(hctx huma.Context) huma.StatusError {
	ctx := hctx.Context()
	_, err := srv.getRunByID(ctx, hctx.Param("runId"))
	if err == nil || errors.Is(err, errInvalidRunID) {
		return nil
	}
	return mapDomainError(ctx, err, "Failed to load run")
}
