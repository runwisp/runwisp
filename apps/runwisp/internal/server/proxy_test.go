// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- parseTrustedProxies ---

func TestParseTrustedProxies_EmptyStringReturnsNil(t *testing.T) {
	opts, err := parseTrustedProxies("")
	require.NoError(t, err)
	assert.Nil(t, opts)
}

func TestParseTrustedProxies_ValidCommaSeparatedCIDRs(t *testing.T) {
	opts, err := parseTrustedProxies("10.0.0.0/8,172.16.0.1")
	require.NoError(t, err)
	require.Len(t, opts, 2)
	assert.Equal(t, "10.0.0.0/8", opts[0].String())
	assert.Equal(t, "172.16.0.1/32", opts[1].String())
}

func TestParseTrustedProxies_InvalidCIDRPropagatesError(t *testing.T) {
	_, err := parseTrustedProxies("10.0.0.0/8,bad-entry")
	assert.Error(t, err)
}

func TestParseTrustedProxies_OnlyBlankEntriesReturnsNil(t *testing.T) {
	opts, err := parseTrustedProxies("  ,  ")
	require.NoError(t, err)
	assert.Nil(t, opts)
}

// --- isFromTrustedProxy ---

func TestIsFromTrustedProxy_NilTrustedReturnsFalse(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	assert.False(t, isFromTrustedProxy(r, nil))
}

func TestIsFromTrustedProxy_EmptySubnetsReturnsFalse(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	assert.False(t, isFromTrustedProxy(r, proxySet{}))
}

func TestIsFromTrustedProxy_IPInSubnetReturnsTrue(t *testing.T) {
	trusted := mustProxies(t, "10.0.0.0/8")
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(r.Context(), peerAddrContextKey, "10.0.0.5:1234")
	r = r.WithContext(ctx)
	assert.True(t, isFromTrustedProxy(r, trusted))
}

func TestIsFromTrustedProxy_IPOutsideSubnetReturnsFalse(t *testing.T) {
	trusted := mustProxies(t, "10.0.0.0/8")
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(r.Context(), peerAddrContextKey, "192.168.1.1:1234")
	r = r.WithContext(ctx)
	assert.False(t, isFromTrustedProxy(r, trusted))
}

func TestIsFromTrustedProxy_FallsBackToRemoteAddrWhenNoContext(t *testing.T) {
	trusted := mustProxies(t, "172.16.0.0/12")
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "172.16.0.1:4321"
	// No peerAddrContextKey in context — must use RemoteAddr.
	assert.True(t, isFromTrustedProxy(r, trusted))
}

func TestIsFromTrustedProxy_ContextAddrTakesPrecedenceOverRemoteAddr(t *testing.T) {
	trusted := mustProxies(t, "10.0.0.0/8")
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	// RemoteAddr is in the trusted range but the real peer (from context) is not.
	r.RemoteAddr = "10.0.0.1:1234"
	ctx := context.WithValue(r.Context(), peerAddrContextKey, "203.0.113.1:5678")
	r = r.WithContext(ctx)
	assert.False(t, isFromTrustedProxy(r, trusted))
}

func mustProxies(t *testing.T, cidrs string) proxySet {
	t.Helper()
	p, err := parseTrustedProxies(cidrs)
	require.NoError(t, err)
	return p
}

// --- clientIPFromForwarded ---

func TestClientIPFromForwarded(t *testing.T) {
	trusted := mustProxies(t, "10.0.0.0/8,127.0.0.1")
	tests := []struct {
		name string
		xff  []string
		want string
	}{
		{"single client", []string{"203.0.113.7"}, "203.0.113.7"},
		{"client-supplied left entries ignored", []string{"1.1.1.1, 203.0.113.7"}, "203.0.113.7"},
		{"trusted hops skipped from the right", []string{"203.0.113.7, 10.1.1.1, 10.2.2.2"}, "203.0.113.7"},
		{"private client outside trusted set is a client", []string{"1.1.1.1, 192.168.1.20"}, "192.168.1.20"},
		{"multiple header lines are joined", []string{"1.1.1.1", "203.0.113.7"}, "203.0.113.7"},
		{"all hops trusted yields leftmost", []string{"10.0.0.9, 10.0.0.1"}, "10.0.0.9"},
		{"malformed hop falls back to peer", []string{"203.0.113.7, bogus"}, ""},
		{"empty", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, clientIPFromForwarded(tt.xff, trusted))
		})
	}
}

// resolveClientIP must not let a client pick its own rate-limit identity.
func TestResolveClientIP(t *testing.T) {
	srv := &Server{trustedProxies: mustProxies(t, "10.0.0.0/8")}
	var got string
	h := srv.resolveClientIP(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.RemoteAddr
	}))
	run := func(peer, xff string) string {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = peer
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
		return got
	}

	assert.Equal(t, "203.0.113.7:4000", run("10.0.0.1:4000", "6.6.6.6, 203.0.113.7"),
		"a spoofed leftmost entry must not replace the hop the proxy appended")
	assert.Equal(t, "192.168.1.20:4000", run("10.0.0.1:4000", "192.168.1.20"),
		"LAN clients behind the proxy keep their own address")
	assert.Equal(t, "203.0.113.9:4000", run("203.0.113.9:4000", "6.6.6.6"),
		"headers from an untrusted peer are ignored")
}
