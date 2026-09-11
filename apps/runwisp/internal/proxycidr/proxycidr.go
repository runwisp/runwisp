// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package proxycidr normalises and validates trusted-proxy CIDR entries. It is
// the single definition shared by the [daemon] trusted_proxies TOML key
// (validated at config load) and the RUNWISP_TRUSTED_PROXIES env var (parsed by
// the server's proxy middleware), so both reject the same catch-all ranges.
package proxycidr

import (
	"fmt"
	"net"
	"strings"
)

// Normalize validates and normalises one trusted-proxy entry. It returns
// ("", nil) for a blank entry, the normalised CIDR (a bare IP gains a host
// mask) on success, or an error for an invalid or catch-all range. Ranges that
// trust the entire address space (0.0.0.0/0, ::/0, or an IPv4-mapped IPv6
// prefix that folds to one) are rejected because they would let any client
// spoof X-Forwarded-For and defeat every IP-based check.
func Normalize(raw string) (string, error) {
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
		return "", fmt.Errorf("invalid CIDR %q: %w", cidr, err)
	}
	ones, bits := ipNet.Mask.Size()
	if ones == 0 && bits != 0 {
		return "", fmt.Errorf("%q trusts the entire address space, which defeats spoofing protection", cidr)
	}
	// net.IPNet.Contains folds an IPv4-mapped IPv6 network (e.g. ::ffff:0:0/96)
	// down to its last 4 mask bytes before comparing, so a /96-or-shorter prefix
	// in that form covers every IPv4 address despite ones != 0 above.
	if bits == 8*net.IPv6len && ipNet.IP.To4() != nil && ones <= 96 {
		return "", fmt.Errorf("%q trusts the entire address space, which defeats spoofing protection", cidr)
	}
	return cidr, nil
}
