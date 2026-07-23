// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStreamLimiter_PerIPCap(t *testing.T) {
	l := newStreamLimiter(100, 2)

	r1, ok := l.acquire("203.0.113.1")
	assert.True(t, ok)
	r2, ok := l.acquire("203.0.113.1")
	assert.True(t, ok)
	_, ok = l.acquire("203.0.113.1")
	assert.False(t, ok, "third concurrent stream from same IP should be refused")

	// A different IP is unaffected.
	r3, ok := l.acquire("203.0.113.2")
	assert.True(t, ok)

	r1()
	_, ok = l.acquire("203.0.113.1")
	assert.True(t, ok, "after release, the IP can acquire again")

	r2()
	r3()
}

func TestStreamLimiter_GlobalCap(t *testing.T) {
	l := newStreamLimiter(2, 100)

	r1, ok := l.acquire("a")
	assert.True(t, ok)
	r2, ok := l.acquire("b")
	assert.True(t, ok)
	_, ok = l.acquire("c")
	assert.False(t, ok, "third concurrent stream globally should be refused")

	r1()
	r2()
}

func TestStreamLimiter_Middleware503(t *testing.T) {
	l := newStreamLimiter(1, 1)

	entered := make(chan struct{})
	called := 0
	h := l.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		close(entered)
		// hold the slot until we let the next request through
		<-r.Context().Done()
	}))

	holdReq := httptest.NewRequest("GET", "/", nil)
	holdCtx, cancel := context.WithCancel(holdReq.Context())
	holdReq = holdReq.WithContext(context.WithValue(holdCtx, peerAddrContextKey, "127.0.0.1:1"))
	holdW := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		h.ServeHTTP(holdW, holdReq)
		close(done)
	}()

	// Wait for the goroutine to enter the handler.
	<-entered

	overReq := httptest.NewRequest("GET", "/", nil)
	overReq = overReq.WithContext(context.WithValue(overReq.Context(), peerAddrContextKey, "127.0.0.2:1"))
	overW := httptest.NewRecorder()
	h.ServeHTTP(overW, overReq)

	assert.Equal(t, http.StatusServiceUnavailable, overW.Code)
	assert.Equal(t, 1, called, "second request should have been refused before reaching the handler")

	cancel()
	<-done
}
