// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package netguard

import (
	"net"
	"testing"
)

func TestRejectNonPublicIP_Blocked(t *testing.T) {
	// One representative address from every denied prefix.
	blocked := []string{
		"0.0.0.0", "0.1.2.3",
		"10.0.0.1", "10.255.255.255",
		"100.64.0.1", "100.100.100.200", // CGNAT incl. Alibaba metadata
		"127.0.0.1",
		"169.254.0.1", "169.254.169.254", // link-local incl. cloud metadata
		"172.16.0.1", "172.31.255.255",
		"192.0.0.1", "192.0.0.192", // IETF assignments incl. OCI metadata
		"192.0.2.1",                    // TEST-NET-1
		"192.88.99.1",                  // 6to4 relay anycast
		"192.168.1.1",                  // private
		"198.18.0.1",                   // benchmarking
		"198.51.100.1",                 // TEST-NET-2
		"203.0.113.1",                  // TEST-NET-3
		"224.0.0.1",                    // multicast
		"240.0.0.1", "255.255.255.255", // reserved / broadcast
		"::", "::1",
		"64:ff9b::1",         // NAT64
		"100::1",             // discard-only
		"2001:db8::1",        // documentation
		"2002::1",            // 6to4
		"fc00::1", "fd12::1", // ULA
		"fe80::1",                             // link-local
		"ff02::1",                             // multicast
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", // IPv4-mapped forms
	}
	for _, s := range blocked {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("test bug: unparseable %q", s)
		}
		if err := RejectNonPublicIP(ip); err == nil {
			t.Errorf("RejectNonPublicIP(%s) = nil, want blocked", s)
		}
	}
}

func TestRejectNonPublicIP_Allowed(t *testing.T) {
	allowed := []string{
		"1.1.1.1", "8.8.8.8", "203.0.114.1", "198.20.0.1",
		"2606:4700:4700::1111", // Cloudflare v6
		"2001:4860:4860::8888", // Google v6
	}
	for _, s := range allowed {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("test bug: unparseable %q", s)
		}
		if err := RejectNonPublicIP(ip); err != nil {
			t.Errorf("RejectNonPublicIP(%s) = %v, want allowed", s, err)
		}
	}
	if err := RejectNonPublicIP(nil); err == nil {
		t.Errorf("RejectNonPublicIP(nil) = nil, want error")
	}
}
