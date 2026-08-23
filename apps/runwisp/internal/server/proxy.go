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

// parseTrustedProxies parses RUNWISP_TRUSTED_PROXIES as a comma-separated list of CIDR ranges.
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
// RUNWISP_TRUSTED_PROXIES. Returns ("", nil) for blank entries, an error for
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
		return "", fmt.Errorf("RUNWISP_TRUSTED_PROXIES: invalid CIDR %q: %w", cidr, err)
	}
	ones, bits := ipNet.Mask.Size()
	if ones == 0 && bits != 0 {
		return "", fmt.Errorf("RUNWISP_TRUSTED_PROXIES rejects %q: trusting the entire address space defeats spoofing protection", cidr)
	}
	// net.IPNet.Contains folds an IPv4-mapped IPv6 network (e.g. ::ffff:0:0/96)
	// down to its last 4 mask bytes before comparing, so a /96-or-shorter prefix
	// in that form covers every IPv4 address despite ones != 0 above.
	if bits == 8*net.IPv6len {
		if ipNet.IP.To4() != nil && ones <= 96 {
			return "", fmt.Errorf("RUNWISP_TRUSTED_PROXIES rejects %q: trusting the entire address space defeats spoofing protection", cidr)
		}
	}
	return cidr, nil
}

// isProxiedRequest reports whether the request looks relayed rather than
// direct: it carries a hop header, or its peer is a configured trusted proxy.
//
// It exists because a loopback peer is not proof of a local client. The
// documented public-exposure setup puts nginx / Caddy / Traefik / a Cloudflare
// Tunnel on the same host forwarding to 127.0.0.1, so every internet request
// arrives from loopback. Gates that mean "only a process on this machine" must
// therefore reject proxied requests as well as non-loopback ones.
//
// Header presence is a heuristic, not a proof — a bare `proxy_pass` with no
// proxy_set_header sends none of them — but every common proxy sets at least
// one, and the local launcher probe sets none, so it closes the realistic cases
// without costing the port-conflict UX.
func isProxiedRequest(r *http.Request, trusted *xff.Options) bool {
	for _, h := range forwardedHeaders {
		if r.Header.Get(h) != "" {
			return true
		}
	}
	return isFromTrustedProxy(r, trusted)
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
	ip := net.ParseIP(hostFromAddr(addr))
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
