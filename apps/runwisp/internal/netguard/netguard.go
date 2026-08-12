// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package netguard is the single SSRF gate shared by every outbound request
// the daemon makes on behalf of an untrusted peer: HTTP task execution
// (internal/executor) and control-plane log uploads (internal/cloud/logarchive).
//
// It answers exactly one question — "is this IP a publicly routable unicast
// address?" — and rejects everything else: loopback, private, link-local,
// CGNAT/shared, multicast, documentation, benchmarking, and every other
// special-use or reserved range. The default is deny: an address only passes
// if it is a normal global-unicast address that matches none of the blocked
// prefixes below.
package netguard

import (
	"fmt"
	"net"
)

// deniedCIDRs are the special-use / non-publicly-routable prefixes an outbound
// peer request must never reach. Covers IANA IPv4 and IPv6 special-purpose
// registries plus the CGNAT metadata endpoints (100.100.100.200, 192.0.0.192)
// that live inside these ranges. IPv4-mapped IPv6 is normalized to v4 before
// matching, so the v4 rows also cover ::ffff:a.b.c.d forms.
var deniedCIDRs = mustParseCIDRs(
	// --- IPv4 ---
	"0.0.0.0/8",       // "this host on this network" (RFC 1122)
	"10.0.0.0/8",      // private (RFC 1918)
	"100.64.0.0/10",   // CGNAT / shared address space (RFC 6598) — incl. Alibaba metadata 100.100.100.200
	"127.0.0.0/8",     // loopback
	"169.254.0.0/16",  // link-local — incl. cloud metadata 169.254.169.254
	"172.16.0.0/12",   // private (RFC 1918)
	"192.0.0.0/24",    // IETF protocol assignments — incl. OCI metadata 192.0.0.192
	"192.0.2.0/24",    // documentation TEST-NET-1
	"192.88.99.0/24",  // 6to4 relay anycast (deprecated)
	"192.168.0.0/16",  // private (RFC 1918)
	"198.18.0.0/15",   // benchmarking (RFC 2544)
	"198.51.100.0/24", // documentation TEST-NET-2
	"203.0.113.0/24",  // documentation TEST-NET-3
	"224.0.0.0/4",     // multicast
	"240.0.0.0/4",     // reserved for future use (incl. 255.255.255.255 broadcast)
	// --- IPv6 ---
	"::/128",         // unspecified
	"::1/128",        // loopback
	"64:ff9b::/96",   // NAT64 well-known prefix
	"64:ff9b:1::/48", // NAT64 local-use
	"100::/64",       // discard-only
	"2001::/23",      // IETF protocol assignments (incl. Teredo 2001::/32)
	"2001:db8::/32",  // documentation
	"2002::/16",      // 6to4
	"fc00::/7",       // unique-local (ULA)
	"fe80::/10",      // link-local unicast
	"ff00::/8",       // multicast
)

// RejectNonPublicIP returns an error unless ip is a publicly routable unicast
// address. It is the one validator every SSRF-sensitive dialer must call on
// each resolved IP before connecting.
func RejectNonPublicIP(ip net.IP) error {
	if ip == nil {
		return fmt.Errorf("nil IP")
	}
	// Normalize IPv4-mapped IPv6 (::ffff:a.b.c.d) to its v4 form so the v4
	// prefixes below match and stdlib predicates behave.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	for _, block := range deniedCIDRs {
		if block.Contains(ip) {
			return fmt.Errorf("address %s is in blocked range %s", ip, block)
		}
	}
	// Belt-and-suspenders: reject anything the stdlib flags as non-global even
	// if a prefix above was somehow missed (e.g. a future stdlib change).
	if !ip.IsGlobalUnicast() {
		return fmt.Errorf("address %s is not global unicast", ip)
	}
	return nil
}

func mustParseCIDRs(cidrs ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic("netguard: bad CIDR " + c + ": " + err.Error())
		}
		out = append(out, n)
	}
	return out
}
