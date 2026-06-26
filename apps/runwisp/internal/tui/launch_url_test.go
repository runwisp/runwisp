// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package tui

import "testing"

func TestIsInsecureRemoteURL(t *testing.T) {
	cases := []struct {
		name string
		base string
		want bool
	}{
		{"https remote is safe", "https://runwisp.example.com", false},
		{"http loopback name is safe", "http://localhost:9477", false},
		{"http loopback ip is safe", "http://127.0.0.1:9477", false},
		{"http ipv6 loopback is safe", "http://[::1]:9477", false},
		{"http LAN host is insecure", "http://192.168.1.10:9477", true},
		{"http public host is insecure", "http://runwisp.example.com", true},
		{"empty is safe", "", false},
		{"https LAN host is safe", "https://192.168.1.10:9477", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isInsecureRemoteURL(tc.base); got != tc.want {
				t.Fatalf("isInsecureRemoteURL(%q) = %v, want %v", tc.base, got, tc.want)
			}
		})
	}
}

// TestOpenLaunchURL_InsecureRemoteShowsConfirm verifies that opening the Web UI
// over a plain-HTTP remote connection is gated behind a confirmation dialog
// rather than firing the browser (and leaking a session ticket) immediately.
func TestOpenLaunchURL_InsecureRemoteShowsConfirm(t *testing.T) {
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")

	m := newTestModel(nil)
	m.launchTicketFunc = func() (string, error) { return "tkt", nil }
	m.info.ListenURL = "http://192.168.1.10:9477"

	cmd := m.openLaunchURL("/")
	if cmd != nil {
		t.Fatal("insecure remote launch must defer to a confirm dialog, not return a launch cmd")
	}
	if !m.dialogs.HasConfirm() {
		t.Fatal("insecure remote launch must raise a confirmation dialog")
	}
}

// TestOpenLaunchURL_SecureBaseOpensDirectly verifies loopback / https bases
// skip the confirmation and produce a launch command immediately.
func TestOpenLaunchURL_SecureBaseOpensDirectly(t *testing.T) {
	stubCanOpenBrowser(t, false)

	for _, base := range []string{"http://localhost:9477", "https://192.168.1.10:9477"} {
		m := newTestModel(nil)
		m.launchTicketFunc = func() (string, error) { return "tkt", nil }
		m.info.ListenURL = base

		cmd := m.openLaunchURL("/")
		if cmd == nil {
			t.Fatalf("secure base %q must open directly", base)
		}
		if m.dialogs.HasConfirm() {
			t.Fatalf("secure base %q must not raise a confirmation dialog", base)
		}
	}
}
