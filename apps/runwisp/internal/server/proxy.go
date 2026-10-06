// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"

	"github.com/runwisp/runwisp/internal/proxycidr"
)

// proxySet is the parsed RUNWISP_TRUSTED_PROXIES / [daemon] trusted_proxies
// list. A nil set means no proxy is trusted.
type proxySet []*net.IPNet

func (p proxySet) contains(ip net.IP) bool {
	for _, n := range p {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// parseTrustedProxies parses RUNWISP_TRUSTED_PROXIES as a comma-separated list of CIDR ranges.
// CIDRs that effectively trust the entire internet (0.0.0.0/0 or ::/0) are
// rejected to prevent silent spoofing of X-Forwarded-For: any IP-based check
// (rate limiting, loopback detection) would be bypassable when every client is
// "a trusted proxy".
func parseTrustedProxies(env string) (proxySet, error) {
	var set proxySet
	for raw := range strings.SplitSeq(env, ",") {
		cidr, err := proxycidr.Normalize(raw)
		if err != nil {
			return nil, fmt.Errorf("RUNWISP_TRUSTED_PROXIES: %w", err)
		}
		if cidr == "" {
			continue
		}
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("RUNWISP_TRUSTED_PROXIES: %w", err)
		}
		set = append(set, ipNet)
	}
	return set, nil
}

// clientIPFromForwarded resolves the real client behind trusted proxies. It
// walks X-Forwarded-For from the right (the hop the nearest proxy appended) and
// returns the first address that is not itself a trusted proxy. The left end of
// the header is whatever the client sent, so it is only reached when every
// hop to its right is a trusted proxy. Private addresses count as clients:
// a LAN host behind the proxy is not a proxy, so it gets its own identity.
// It returns "" when the header carries nothing usable.
func clientIPFromForwarded(xff []string, trusted proxySet) string {
	var hops []string
	for _, v := range xff {
		hops = append(hops, strings.Split(v, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		hop := strings.TrimSpace(hops[i])
		ip := net.ParseIP(hop)
		if ip == nil {
			return "" // a malformed hop is untrustworthy: fall back to the peer
		}
		if !trusted.contains(ip) || i == 0 {
			return ip.String()
		}
	}
	return ""
}

type clientIPKey struct{}

// resolveClientIP rewrites r.RemoteAddr to the real client address when the
// TCP peer is a trusted proxy and stores the resolved host in the context.
// Without a trusted peer the headers are ignored. savePeerAddr has already
// captured the raw peer for the checks that must not trust any header.
func (srv *Server) resolveClientIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isFromTrustedProxy(r, srv.trustedProxies) {
			if ip := clientIPFromForwarded(r.Header.Values("X-Forwarded-For"), srv.trustedProxies); ip != "" {
				_, port, _ := net.SplitHostPort(r.RemoteAddr)
				r.RemoteAddr = net.JoinHostPort(ip, port)
			}
		}
		ctx := context.WithValue(r.Context(), clientIPKey{}, hostFromAddr(r.RemoteAddr))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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
func isProxiedRequest(r *http.Request, trusted proxySet) bool {
	return slices.ContainsFunc(forwardedHeaders, func(h string) bool { return r.Header.Get(h) != "" }) ||
		isFromTrustedProxy(r, trusted)
}

// isFromTrustedProxy reports whether the request's TCP peer is within the
// configured trusted-proxy CIDR set. It uses the original peer address
// (captured by savePeerAddr) so that an attacker cannot inject a header to
// pretend to be a trusted proxy.
func isFromTrustedProxy(r *http.Request, trusted proxySet) bool {
	if len(trusted) == 0 {
		return false
	}
	addr := r.RemoteAddr
	if peer, ok := r.Context().Value(peerAddrContextKey).(string); ok {
		addr = peer
	}
	ip := net.ParseIP(hostFromAddr(addr))
	return ip != nil && trusted.contains(ip)
}
