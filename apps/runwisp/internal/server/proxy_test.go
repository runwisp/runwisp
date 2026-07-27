// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sebest/xff"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- normalizeTrustProxyCIDR ---

func TestNormalizeTrustProxyCIDR_BlankInput(t *testing.T) {
	cidr, err := normalizeTrustProxyCIDR("   ")
	require.NoError(t, err)
	assert.Equal(t, "", cidr)
}

func TestNormalizeTrustProxyCIDR_ExactIPv4AppendsHostMask(t *testing.T) {
	cidr, err := normalizeTrustProxyCIDR("10.0.0.1")
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.1/32", cidr)
}

func TestNormalizeTrustProxyCIDR_ExactIPv6AppendsHostMask(t *testing.T) {
	cidr, err := normalizeTrustProxyCIDR("2001:db8::1")
	require.NoError(t, err)
	assert.Equal(t, "2001:db8::1/128", cidr)
}

func TestNormalizeTrustProxyCIDR_ValidCIDRPassesThrough(t *testing.T) {
	cidr, err := normalizeTrustProxyCIDR("192.168.1.0/24")
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.0/24", cidr)
}

func TestNormalizeTrustProxyCIDR_CatchAllIPv4Rejected(t *testing.T) {
	_, err := normalizeTrustProxyCIDR("0.0.0.0/0")
	assert.Error(t, err)
}

func TestNormalizeTrustProxyCIDR_CatchAllIPv6Rejected(t *testing.T) {
	_, err := normalizeTrustProxyCIDR("::/0")
	assert.Error(t, err)
}

func TestNormalizeTrustProxyCIDR_BadCIDRReturnsError(t *testing.T) {
	_, err := normalizeTrustProxyCIDR("not-an-ip/24")
	assert.Error(t, err)
}

// --- parseTrustedProxies ---

func TestParseTrustedProxies_EmptyStringReturnsNil(t *testing.T) {
	opts, err := parseTrustedProxies("")
	require.NoError(t, err)
	assert.Nil(t, opts)
}

func TestParseTrustedProxies_ValidCommaSeparatedCIDRs(t *testing.T) {
	opts, err := parseTrustedProxies("10.0.0.0/8,172.16.0.1")
	require.NoError(t, err)
	require.NotNil(t, opts)
	assert.Equal(t, []string{"10.0.0.0/8", "172.16.0.1/32"}, opts.AllowedSubnets)
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
	assert.False(t, isFromTrustedProxy(r, &xff.Options{}))
}

func TestIsFromTrustedProxy_IPInSubnetReturnsTrue(t *testing.T) {
	trusted := &xff.Options{AllowedSubnets: []string{"10.0.0.0/8"}}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(r.Context(), peerAddrContextKey, "10.0.0.5:1234")
	r = r.WithContext(ctx)
	assert.True(t, isFromTrustedProxy(r, trusted))
}

func TestIsFromTrustedProxy_IPOutsideSubnetReturnsFalse(t *testing.T) {
	trusted := &xff.Options{AllowedSubnets: []string{"10.0.0.0/8"}}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(r.Context(), peerAddrContextKey, "192.168.1.1:1234")
	r = r.WithContext(ctx)
	assert.False(t, isFromTrustedProxy(r, trusted))
}

func TestIsFromTrustedProxy_FallsBackToRemoteAddrWhenNoContext(t *testing.T) {
	trusted := &xff.Options{AllowedSubnets: []string{"172.16.0.0/12"}}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "172.16.0.1:4321"
	// No peerAddrContextKey in context — must use RemoteAddr.
	assert.True(t, isFromTrustedProxy(r, trusted))
}

func TestIsFromTrustedProxy_ContextAddrTakesPrecedenceOverRemoteAddr(t *testing.T) {
	trusted := &xff.Options{AllowedSubnets: []string{"10.0.0.0/8"}}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	// RemoteAddr is in the trusted range but the real peer (from context) is not.
	r.RemoteAddr = "10.0.0.1:1234"
	ctx := context.WithValue(r.Context(), peerAddrContextKey, "203.0.113.1:5678")
	r = r.WithContext(ctx)
	assert.False(t, isFromTrustedProxy(r, trusted))
}
