// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"slices"
	"testing"
)

// TestDaemonSpawnArgs_CarriesHostAndSocket: the spawned daemon must inherit
// --host and --socket so it binds where the launcher probed instead of silently
// re-defaulting to loopback / the default socket path.
func TestDaemonSpawnArgs_CarriesHostAndSocket(t *testing.T) {
	f := Flags{
		CfgFile: "cfg.toml",
		DataDir: "/data",
		Port:    9477,
		Host:    "0.0.0.0",
		Socket:  "/run/custom.sock",
	}
	args := daemonSpawnArgs([]string{"daemon"}, f)

	if len(args) == 0 || args[0] != "daemon" {
		t.Fatalf("expected leading subcommand, got %v", args)
	}
	assertFlagValue(t, args, "--host", "0.0.0.0")
	assertFlagValue(t, args, "--socket", "/run/custom.sock")
	assertFlagValue(t, args, "--port", "9477")
}

func TestDaemonSpawnArgs_OmitsEmptySocket(t *testing.T) {
	args := daemonSpawnArgs([]string{"station", "--no-tui"}, Flags{Host: "127.0.0.1"})
	if slices.Contains(args, "--socket") {
		t.Fatalf("empty socket should not be passed, got %v", args)
	}
	assertFlagValue(t, args, "--host", "127.0.0.1")
}

func assertFlagValue(t *testing.T, args []string, flag, want string) {
	t.Helper()
	for i, a := range args {
		if a == flag {
			if i+1 >= len(args) {
				t.Fatalf("%s has no value in %v", flag, args)
			}
			if args[i+1] != want {
				t.Fatalf("%s: got %q want %q", flag, args[i+1], want)
			}
			return
		}
	}
	t.Fatalf("%s not present in %v", flag, args)
}
