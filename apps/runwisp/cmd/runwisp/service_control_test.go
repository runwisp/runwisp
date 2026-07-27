// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShouldDelegateStop(t *testing.T) {
	tests := []struct {
		name string
		st   autostart.Status
		want bool
	}{
		{
			name: "managed and running delegates",
			st:   autostart.Status{UnitExists: true, UnitManaged: true, Running: true},
			want: true,
		},
		{
			name: "managed but stopped falls back to PID path",
			st:   autostart.Status{UnitExists: true, UnitManaged: true, Running: false},
			want: false,
		},
		{
			name: "hand-written unit is never delegated",
			st:   autostart.Status{UnitExists: true, UnitManaged: false, Running: true},
			want: false,
		},
		{
			name: "no unit installed",
			st:   autostart.Status{Running: true},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, shouldDelegateStop(tt.st))
		})
	}
}

func TestShouldDelegateRestart(t *testing.T) {
	tests := []struct {
		name string
		st   autostart.Status
		want bool
	}{
		{
			name: "managed and running delegates",
			st:   autostart.Status{UnitExists: true, UnitManaged: true, Running: true},
			want: true,
		},
		{
			name: "managed, stopped but enabled still delegates",
			st:   autostart.Status{UnitExists: true, UnitManaged: true, Autostart: true},
			want: true,
		},
		{
			name: "managed, stopped and disabled falls back",
			st:   autostart.Status{UnitExists: true, UnitManaged: true},
			want: false,
		},
		{
			name: "hand-written unit is never delegated",
			st:   autostart.Status{UnitExists: true, Running: true, Autostart: true},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, shouldDelegateRestart(tt.st))
		})
	}
}

func TestServiceManagerName(t *testing.T) {
	assert.Equal(t, "systemd", serviceManagerName(autostart.Status{OS: "linux"}))
	assert.Equal(t, "launchd", serviceManagerName(autostart.Status{OS: "darwin"}))
	assert.Equal(t, "the service manager", serviceManagerName(autostart.Status{OS: "plan9"}))
}

func TestStopWaitTimeout(t *testing.T) {
	t.Parallel()

	t.Run("unreadable config floors at 15s", func(t *testing.T) {
		t.Parallel()
		f := Flags{CfgFile: filepath.Join(t.TempDir(), "missing.toml")}
		assert.Equal(t, 15*time.Second, stopWaitTimeout(f))
	})

	t.Run("long shutdown_timeout gets headroom", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "runwisp.toml")
		require.NoError(t, os.WriteFile(path, []byte("[daemon]\nshutdown_timeout = \"60s\"\n\n[tasks.t]\nrun = \"echo hi\"\n"), 0o600))
		assert.Equal(t, 65*time.Second, stopWaitTimeout(Flags{CfgFile: path}))
	})

	t.Run("short shutdown_timeout still floors at 15s", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "runwisp.toml")
		require.NoError(t, os.WriteFile(path, []byte("[daemon]\nshutdown_timeout = \"2s\"\n\n[tasks.t]\nrun = \"echo hi\"\n"), 0o600))
		assert.Equal(t, 15*time.Second, stopWaitTimeout(Flags{CfgFile: path}))
	})
}
