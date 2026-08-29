// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"sync"
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

// streamClientIPFromCtx returns the real TCP peer's IP, ignoring proxy headers.
// We deliberately use peerAddr (captured before the trusted-proxy XFF
// middleware) so that an attacker cannot bypass the cap by rotating
// X-Forwarded-For values. It reads from context because huma SSE handlers do
// not receive the *http.Request directly.
func streamClientIPFromCtx(ctx context.Context) string {
	if peer, ok := ctx.Value(peerAddrContextKey).(string); ok {
		return hostFromAddr(peer)
	}
	return ""
}
