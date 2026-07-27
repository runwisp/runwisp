// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestIsLoopbackAddr(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:6060", true},
		{"localhost:6060", true},
		{"[::1]:6060", true},
		{"0.0.0.0:6060", false},
		{":6060", false}, // empty host = all interfaces
		{"192.168.1.10:6060", false},
		{"example.com:6060", false},
		{"garbage", false}, // no port
	}
	for _, tc := range cases {
		if got := isLoopbackAddr(tc.addr); got != tc.want {
			t.Errorf("isLoopbackAddr(%q) = %v, want %v", tc.addr, got, tc.want)
		}
	}
}

func TestStartDebugServer_OffWhenUnset(t *testing.T) {
	// t.Setenv can't unset; this test environment shouldn't carry the var, but
	// guard anyway by asserting nil only when truly unset.
	if v, ok := os.LookupEnv(DebugAddrEnv); ok && v != "" {
		t.Skip("RUNWISP_DEBUG_ADDR set in environment; skipping unset-path test")
	}
	if d := startDebugServer(); d != nil {
		d.Close()
		t.Fatal("expected nil debug server when RUNWISP_DEBUG_ADDR is unset")
	}
}

func TestStartDebugServer_RefusesNonLoopback(t *testing.T) {
	t.Setenv(DebugAddrEnv, "0.0.0.0:0")
	if d := startDebugServer(); d != nil {
		d.Close()
		t.Fatal("expected nil debug server for a non-loopback address")
	}
}

func TestStartDebugServer_ServesPprofOnLoopback(t *testing.T) {
	t.Setenv(DebugAddrEnv, "127.0.0.1:0") // :0 → OS picks a free port
	d := startDebugServer()
	if d == nil {
		t.Fatal("expected a running debug server on a loopback address")
	}
	defer d.Close()

	resp, err := (&http.Client{Timeout: 2 * time.Second}).Get("http://" + d.addr + "/debug/pprof/heap?debug=1")
	if err != nil {
		t.Fatalf("GET heap profile: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("heap profile status = %d, want 200", resp.StatusCode)
	}
}
