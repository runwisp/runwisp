// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func ctxWithPeer(addr string) context.Context {
	return context.WithValue(context.Background(), peerAddrContextKey, addr)
}

func ctxLocalTrusted(addr string) context.Context {
	return context.WithValue(ctxWithPeer(addr), localTrustedKey{}, true)
}

func TestStreamLimiter_PerIPCap(t *testing.T) {
	l := newStreamLimiter(100, 2)

	r1, ok := l.acquire(ctxWithPeer("203.0.113.1:1"))
	assert.True(t, ok)
	r2, ok := l.acquire(ctxWithPeer("203.0.113.1:2"))
	assert.True(t, ok)
	_, ok = l.acquire(ctxWithPeer("203.0.113.1:3"))
	assert.False(t, ok, "third concurrent stream from same IP should be refused")

	// A different IP is unaffected.
	r3, ok := l.acquire(ctxWithPeer("203.0.113.2:1"))
	assert.True(t, ok)

	r1()
	_, ok = l.acquire(ctxWithPeer("203.0.113.1:4"))
	assert.True(t, ok, "after release, the IP can acquire again")

	r2()
	r3()
}

func TestStreamLimiter_GlobalCap(t *testing.T) {
	l := newStreamLimiter(2, 100)

	r1, ok := l.acquire(ctxWithPeer("a:1"))
	assert.True(t, ok)
	r2, ok := l.acquire(ctxWithPeer("b:1"))
	assert.True(t, ok)
	_, ok = l.acquire(ctxWithPeer("c:1"))
	assert.False(t, ok, "third concurrent stream globally should be refused")

	r1()
	r2()
}

// TestStreamLimiter_LocalTrustedExemptFromPerIPCap guards against a bug where
// every Unix-socket client collapsed into one shared per-IP bucket: a local
// UnixConn's RemoteAddr() is empty for an unnamed client socket, so all local
// TUI/CLI streams keyed on "" and capped the whole machine's local clients
// combined at maxPerIP. Local-trusted (PEERCRED-verified) connections must
// still count toward the global cap, but not toward any shared per-IP cap.
func TestStreamLimiter_LocalTrustedExemptFromPerIPCap(t *testing.T) {
	l := newStreamLimiter(100, 2)

	var releases []func()
	for i := 0; i < 5; i++ {
		r, ok := l.acquire(ctxLocalTrusted(""))
		assert.True(t, ok, "local-trusted streams must not be capped by the per-IP sub-limit")
		releases = append(releases, r)
	}

	// The global cap still applies to local-trusted streams.
	l2 := newStreamLimiter(5, 100)
	for i := 0; i < 5; i++ {
		_, ok := l2.acquire(ctxLocalTrusted(""))
		assert.True(t, ok)
	}
	_, ok := l2.acquire(ctxLocalTrusted(""))
	assert.False(t, ok, "the global cap must still bound local-trusted streams")

	for _, r := range releases {
		r()
	}
}
