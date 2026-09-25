// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/autostart"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func taskResponse(name string, kind model.TaskKind, manualTrigger bool) model.TaskResponse {
	return model.TaskResponse{Task: model.Task{Name: name, Kind: kind, ManualTrigger: manualTrigger}}
}

func TestResolveTargets(t *testing.T) {
	tasks := []model.TaskResponse{
		taskResponse("web", model.KindService, true),
		taskResponse("worker", model.KindService, true),
		taskResponse("locked-svc", model.KindService, false),
		taskResponse("backup", model.KindTask, true),
		taskResponse("locked-task", model.KindTask, false),
	}
	const runID = "01J8Z3K9QK6VN8XG2R5F7T1C4M"

	t.Run("literal name resolves regardless of lock", func(t *testing.T) {
		got, err := resolveTargets([]string{"web", "locked-svc"}, tasks, false)
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, resolvedTarget{kind: targetTaskKind, name: "web", isService: true}, got[0])
		assert.Equal(t, resolvedTarget{kind: targetTaskKind, name: "locked-svc", isService: true}, got[1])
	})

	t.Run("glob matches across both kinds and skips locked entries", func(t *testing.T) {
		got, err := resolveTargets([]string{"*"}, tasks, false)
		require.NoError(t, err)
		names := make([]string, len(got))
		for i, g := range got {
			names[i] = g.name
		}
		assert.ElementsMatch(t, []string{"web", "worker", "backup"}, names)
	})

	t.Run("glob matching only locked entries is an error", func(t *testing.T) {
		_, err := resolveTargets([]string{"locked-*"}, tasks, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "matched no controllable")
	})

	t.Run("glob with no match at all is an error", func(t *testing.T) {
		_, err := resolveTargets([]string{"nope-*"}, tasks, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"nope-*"`)
	})

	t.Run("invalid pattern is an error", func(t *testing.T) {
		_, err := resolveTargets([]string{"["}, tasks, false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid pattern")
	})

	t.Run("dedupes overlapping literal and glob matches, preserving order", func(t *testing.T) {
		got, err := resolveTargets([]string{"web", "w*"}, tasks, false)
		require.NoError(t, err)
		names := make([]string, len(got))
		for i, g := range got {
			names[i] = g.name
		}
		assert.Equal(t, []string{"web", "worker"}, names)
	})

	t.Run("run ID only resolves as a run target when allowed", func(t *testing.T) {
		got, err := resolveTargets([]string{runID}, tasks, true)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, resolvedTarget{kind: targetRunKind, name: runID}, got[0])
	})

	t.Run("ULID-shaped arg stays a task name when run IDs aren't allowed", func(t *testing.T) {
		got, err := resolveTargets([]string{runID}, tasks, false)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, resolvedTarget{kind: targetTaskKind, name: runID}, got[0])
	})

	t.Run("unknown literal name still produces a task target for the server to 404", func(t *testing.T) {
		got, err := resolveTargets([]string{"nope"}, tasks, true)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, resolvedTarget{kind: targetTaskKind, name: "nope"}, got[0])
	})
}

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
