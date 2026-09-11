// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/runwisp/runwisp/internal/proxycidr"
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
	for raw := range strings.SplitSeq(env, ",") {
		cidr, err := proxycidr.Normalize(raw)
		if err != nil {
			return nil, fmt.Errorf("RUNWISP_TRUSTED_PROXIES: %w", err)
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
