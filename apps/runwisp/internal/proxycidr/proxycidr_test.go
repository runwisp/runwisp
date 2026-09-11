// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package proxycidr

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalize_BlankInput(t *testing.T) {
	cidr, err := Normalize("   ")
	require.NoError(t, err)
	assert.Equal(t, "", cidr)
}

func TestNormalize_ExactIPv4AppendsHostMask(t *testing.T) {
	cidr, err := Normalize("10.0.0.1")
	require.NoError(t, err)
	assert.Equal(t, "10.0.0.1/32", cidr)
}

func TestNormalize_ExactIPv6AppendsHostMask(t *testing.T) {
	cidr, err := Normalize("2001:db8::1")
	require.NoError(t, err)
	assert.Equal(t, "2001:db8::1/128", cidr)
}

func TestNormalize_ValidCIDRPassesThrough(t *testing.T) {
	cidr, err := Normalize("192.168.1.0/24")
	require.NoError(t, err)
	assert.Equal(t, "192.168.1.0/24", cidr)
}

func TestNormalize_CatchAllIPv4Rejected(t *testing.T) {
	_, err := Normalize("0.0.0.0/0")
	assert.Error(t, err)
}

func TestNormalize_CatchAllIPv6Rejected(t *testing.T) {
	_, err := Normalize("::/0")
	assert.Error(t, err)
}

func TestNormalize_IPv4MappedCatchAllRejected(t *testing.T) {
	// ::ffff:0:0/96 has ones=96 (not caught by the ones==0 check), but
	// net.IPNet.Contains folds an IPv4-mapped network to its last 4 mask
	// bytes before comparing, so this matches every IPv4 address.
	_, err := Normalize("::ffff:0:0/96")
	require.Error(t, err)

	_, ipNet, parseErr := net.ParseCIDR("::ffff:0:0/96")
	require.NoError(t, parseErr)
	assert.True(t, ipNet.Contains(net.ParseIP("8.8.8.8")), "sanity check: this network really does fold to match any IPv4 address")
}

func TestNormalize_IPv4MappedNarrowRangeAllowed(t *testing.T) {
	// ::ffff:10.0.0.0/104 folds to the equivalent of 10.0.0.0/8 — a
	// legitimately scoped range, not a catch-all — and must be allowed.
	cidr, err := Normalize("::ffff:10.0.0.0/104")
	require.NoError(t, err)
	assert.Equal(t, "::ffff:10.0.0.0/104", cidr)
}

func TestNormalize_BadCIDRReturnsError(t *testing.T) {
	_, err := Normalize("not-an-ip/24")
	assert.Error(t, err)
}
