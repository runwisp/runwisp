// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
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
