// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/sebest/xff"
)

// parseTrustedProxies parses RUNWISP_TRUST_PROXY as a comma-separated list of CIDR ranges.
// It converts it to xff.Options for the proxy middleware. CIDRs that effectively
// trust the entire internet (0.0.0.0/0 or ::/0) are rejected to prevent silent
// spoofing of X-Forwarded-For: any IP-based check (rate limiting, loopback
// detection) would be bypassable when every client is "a trusted proxy".
func parseTrustedProxies(env string) (*xff.Options, error) {
	if env == "" {
		return nil, nil
	}
	var subnets []string
	for _, raw := range strings.Split(env, ",") {
		cidr, err := normalizeTrustProxyCIDR(raw)
		if err != nil {
			return nil, err
		}
		if cidr != "" {
			subnets = append(subnets, cidr)
		}
	}

	if len(subnets) == 0 {
		return nil, nil
	}

	return &xff.Options{
		AllowedSubnets: subnets,
	}, nil
}

// normalizeTrustProxyCIDR validates and normalises one raw entry from
// RUNWISP_TRUST_PROXY. Returns ("", nil) for blank entries, an error for
// invalid or catch-all CIDRs, or the normalised CIDR string otherwise.
func normalizeTrustProxyCIDR(raw string) (string, error) {
	cidr := strings.TrimSpace(raw)
	if cidr == "" {
		return "", nil
	}
	if !strings.Contains(cidr, "/") {
		// Append a host mask if an exact IP was given rather than a CIDR.
		if strings.Contains(cidr, ":") {
			cidr += "/128"
		} else {
			cidr += "/32"
		}
	}
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("RUNWISP_TRUST_PROXY: invalid CIDR %q: %w", cidr, err)
	}
	ones, bits := ipNet.Mask.Size()
	if ones == 0 && bits != 0 {
		return "", fmt.Errorf("RUNWISP_TRUST_PROXY rejects %q: trusting the entire address space defeats spoofing protection", cidr)
	}
	return cidr, nil
}

// isFromTrustedProxy reports whether the request's TCP peer is within the
// configured trusted-proxy CIDR set. It uses the original peer address
// (captured by savePeerAddr) so that an attacker cannot inject a header to
// pretend to be a trusted proxy.
func isFromTrustedProxy(r *http.Request, trusted *xff.Options) bool {
	if trusted == nil || len(trusted.AllowedSubnets) == 0 {
		return false
	}
	addr := r.RemoteAddr
	if peer, ok := r.Context().Value(peerAddrContextKey).(string); ok {
		addr = peer
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, cidr := range trusted.AllowedSubnets {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if ipNet.Contains(ip) {
			return true
		}
	}
	return false
}
