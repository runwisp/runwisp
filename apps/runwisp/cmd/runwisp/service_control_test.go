// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/apiclient"
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

	names := func(ts []model.TaskResponse) []string {
		out := make([]string, len(ts))
		for i, t := range ts {
			out[i] = t.Name
		}
		return out
	}

	t.Run("literal name resolves regardless of lock", func(t *testing.T) {
		got, runs, err := resolveTargets([]string{"web", "locked-svc"}, tasks, false, controllableTargets)
		require.NoError(t, err)
		assert.Equal(t, []string{"web", "locked-svc"}, names(got))
		assert.Empty(t, runs)
	})

	t.Run("glob matches across both kinds and skips locked entries", func(t *testing.T) {
		got, _, err := resolveTargets([]string{"*"}, tasks, false, controllableTargets)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"web", "worker", "backup"}, names(got))
	})

	t.Run("glob matching only locked entries is an error", func(t *testing.T) {
		_, _, err := resolveTargets([]string{"locked-*"}, tasks, false, controllableTargets)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "matched no controllable")
	})

	t.Run("read-only glob includes locked entries", func(t *testing.T) {
		got, _, err := resolveTargets([]string{"locked-*"}, tasks, false, allTargets)
		require.NoError(t, err)
		assert.Equal(t, []string{"locked-svc", "locked-task"}, names(got))

		_, _, err = resolveTargets([]string{"nope-*"}, tasks, false, allTargets)
		require.ErrorContains(t, err, "matched no task or service")
	})

	t.Run("glob with no match at all is an error", func(t *testing.T) {
		_, _, err := resolveTargets([]string{"nope-*"}, tasks, false, controllableTargets)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"nope-*"`)
	})

	t.Run("invalid pattern is an error", func(t *testing.T) {
		_, _, err := resolveTargets([]string{"["}, tasks, false, controllableTargets)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid pattern")
	})

	t.Run("dedupes overlapping literal and glob matches, preserving order", func(t *testing.T) {
		got, _, err := resolveTargets([]string{"web", "w*"}, tasks, false, controllableTargets)
		require.NoError(t, err)
		assert.Equal(t, []string{"web", "worker"}, names(got))
	})

	t.Run("run ID resolves as a run target when allowed", func(t *testing.T) {
		got, runs, err := resolveTargets([]string{runID, runID}, tasks, true, controllableTargets)
		require.NoError(t, err)
		assert.Empty(t, got)
		assert.Equal(t, []string{runID}, runs)
	})

	t.Run("run ID is an unknown name when run IDs aren't allowed", func(t *testing.T) {
		_, _, err := resolveTargets([]string{runID}, tasks, false, controllableTargets)
		require.Error(t, err)
		assert.Contains(t, err.Error(), runID)
	})

	t.Run("unknown literal name is an error with a suggestion", func(t *testing.T) {
		_, _, err := resolveTargets([]string{"wbe"}, tasks, true, controllableTargets)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `Did you mean "web"?`)
	})
}

// pause/resume globs only pick tasks the verb can act on, so `pause '*'`
// doesn't fail on services, cron-less, locked, or held tasks, and `resume '*'`
// reports only the tasks it actually resumed.
func TestResolveTargets_PauseFilters(t *testing.T) {
	cronTask := func(name string, mutate func(*model.TaskResponse)) model.TaskResponse {
		tr := taskResponse(name, model.KindTask, true)
		tr.Cron = "0 3 * * *"
		if mutate != nil {
			mutate(&tr)
		}
		return tr
	}
	pausedAt := time.Date(2026, 9, 29, 14, 0, 0, 0, time.UTC)
	tasks := []model.TaskResponse{
		cronTask("nightly", nil),
		cronTask("paused", func(t *model.TaskResponse) { t.PausedAt = &pausedAt }),
		cronTask("locked", func(t *model.TaskResponse) { t.ManualTrigger = false }),
		cronTask("held", func(t *model.TaskResponse) { t.HeldBy = model.HeldByCron }),
		cronTask("manual", func(t *model.TaskResponse) { t.Cron = "" }),
		cronTask("web", func(t *model.TaskResponse) { t.Kind = model.KindService }),
	}
	names := func(ts []model.TaskResponse) []string {
		out := make([]string, len(ts))
		for i, t := range ts {
			out[i] = t.Name
		}
		return out
	}

	got, _, err := resolveTargets([]string{"*"}, tasks, false, pausableTargets)
	require.NoError(t, err)
	assert.Equal(t, []string{"nightly", "paused"}, names(got))

	got, _, err = resolveTargets([]string{"*"}, tasks, false, pausedTargets)
	require.NoError(t, err)
	assert.Equal(t, []string{"paused"}, names(got))

	_, _, err = resolveTargets([]string{"n*"}, tasks, false, pausedTargets)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "matched no paused task")

	got, _, err = resolveTargets([]string{"web"}, tasks, false, pausableTargets)
	require.NoError(t, err, "a literal name still reaches the server, which explains the refusal")
	assert.Equal(t, []string{"web"}, names(got))
}

func TestControlError_ConflictShowsDetail(t *testing.T) {
	err := &apiclient.HTTPStatusError{
		StatusCode: http.StatusConflict,
		Body:       `{"title":"Conflict","status":409,"detail":"schedule cannot be paused: it is a service"}`,
	}
	got := controlError(err, "", "pause", "web", false)
	assert.EqualError(t, got, `cannot pause "web": schedule cannot be paused: it is a service`)
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

// Stopping a taken-over daemon leaves the box with no scheduler: cron is masked,
// so no job runs until RunWisp is back. The stop used to say only "it will start
// again on the next boot".
func TestStopViaService_WarnsWhenCronIsMasked(t *testing.T) {
	f := Flags{DataDir: t.TempDir()}
	for _, tt := range []struct {
		name string
		st   autostart.Status
		warn bool
	}{
		{"masked by the take-over", autostart.Status{CronUnit: "cron.service", CronMasked: true}, true},
		{"cron came back", autostart.Status{CronUnit: "cron.service", CronActive: true}, false},
		{"never took over", autostart.Status{}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			require.NoError(t, stopViaService(&out, &fakeTakeoverInstaller{}, autostart.InstallOptions{}, tt.st, f))
			if tt.warn {
				assert.Contains(t, out.String(), "cron.service is masked by the RunWisp take-over, so no cron jobs run")
				assert.Contains(t, out.String(), "runwisp restart")
			} else {
				assert.NotContains(t, out.String(), "Warning")
			}
		})
	}
}
