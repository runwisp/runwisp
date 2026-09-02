// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/sse"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/runtime"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatsProvider_GetSystemStats_PopulatesBasicFields(t *testing.T) {
	p := newStatsProvider(&model.DaemonInfo{Version: "vTest"}, time.Now().Add(-2*time.Hour))
	stats := p.GetSystemStats()

	assert.Equal(t, AppName, stats.Name)
	assert.NotEmpty(t, stats.Host)
	assert.NotEmpty(t, stats.OS)
	assert.NotEmpty(t, stats.Arch)
	assert.Greater(t, stats.CPUCores, 0)
	assert.NotEmpty(t, stats.Uptime)
}

func TestStatsProvider_GetDaemonInfo_PassThrough(t *testing.T) {
	info := &model.DaemonInfo{Fingerprint: "fp1"}
	p := newStatsProvider(info, time.Now())
	got := p.GetDaemonInfo()
	assert.Equal(t, "fp1", got.Fingerprint)
}

func TestStatsProvider_GetDaemonInfo_NilFallback(t *testing.T) {
	p := newStatsProvider(nil, time.Now())
	got := p.GetDaemonInfo()
	require.NotNil(t, got, "must always return a non-nil struct, even when no info was supplied")
	assert.Empty(t, got.Fingerprint)
}

func TestFormatUptime(t *testing.T) {
	cases := map[time.Duration]string{
		0:                             "0m",
		30 * time.Second:              "0m",
		5 * time.Minute:               "5m",
		2*time.Hour + 15*time.Minute:  "2h 15m",
		3*24*time.Hour + 4*time.Hour:  "3d 4h 0m",
		24*time.Hour + 30*time.Minute: "1d 0h 30m",
	}
	for d, want := range cases {
		assert.Equal(t, want, formatUptime(d), "%v", d)
	}
}

func TestHumaGetInfo(t *testing.T) {
	srv := &Server{
		stats: newStatsProvider(&model.DaemonInfo{Fingerprint: "test-fp"}, time.Now()),
	}
	out, err := srv.humaGetInfo(context.Background(), &struct{}{})
	require.NoError(t, err)
	assert.Equal(t, "test-fp", out.Body.Fingerprint)
}

func TestHumaGetInfo_SurfacesUpdateStatus(t *testing.T) {
	srv := &Server{
		stats:        newStatsProvider(&model.DaemonInfo{UpdateMethod: "self"}, time.Now()),
		updateStatus: func() (bool, string) { return true, "v9.9.9" },
	}
	out, err := srv.humaGetInfo(context.Background(), &struct{}{})
	require.NoError(t, err)
	assert.True(t, out.Body.UpdateAvailable)
	assert.Equal(t, "v9.9.9", out.Body.LatestVersion)
	assert.Equal(t, "self", out.Body.UpdateMethod)
}

func TestHumaUpdate_UnavailableWithoutHook(t *testing.T) {
	srv := &Server{} // no selfUpdate hook: docker/npm/manual install
	_, err := srv.humaUpdate(context.Background(), &struct{}{})
	require.Error(t, err)
}

func TestHumaUpdate_HookError(t *testing.T) {
	srv := &Server{
		selfUpdate: func() (model.UpdateResult, error) {
			return model.UpdateResult{}, errors.New("checksum mismatch")
		},
	}
	_, err := srv.humaUpdate(context.Background(), &struct{}{})
	require.Error(t, err)
}

func TestHumaUpdate_InvokesHook(t *testing.T) {
	called := false
	srv := &Server{
		selfUpdate: func() (model.UpdateResult, error) {
			called = true
			return model.UpdateResult{OldVersion: "0.16.0", NewVersion: "0.17.0"}, nil
		},
	}
	out, err := srv.humaUpdate(context.Background(), &struct{}{})
	require.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, "0.17.0", out.Body.NewVersion)
}

// TestHumaGetInfo_TasksComeFromTheLiveRegistry is the reporting half of the cron
// hold releasing itself. The scheduler picking a job back up is invisible if
// /api/daemon keeps serving the task list built at boot: `runwisp status` and the
// TUI header both read it, so they would go on saying "held by cron" for as long
// as the daemon ran, long after RunWisp took the job over. A reload's added and
// removed tasks were stale on those two surfaces for the same reason.
func TestHumaGetInfo_TasksComeFromTheLiveRegistry(t *testing.T) {
	held := &model.Task{
		Name: "backup", Cron: "0 3 * * *", Run: "backup.sh",
		Source: model.SourceCron, SourceFile: "/etc/cron.d/backup",
		HeldBy: model.HeldByCron,
	}
	registry := runtime.NewTaskRegistry(map[string]*model.Task{"backup": held})
	srv := &Server{
		tasks: registry,
		stats: newStatsProvider(&model.DaemonInfo{
			Tasks: []model.TaskBrief{model.NewTaskBrief(held)},
		}, time.Now()),
	}

	out, err := srv.humaGetInfo(context.Background(), &struct{}{})
	require.NoError(t, err)
	require.Len(t, out.Body.Tasks, 1)
	require.Equal(t, model.HeldByCron, out.Body.Tasks[0].HeldBy)

	// cron retires: the watcher swaps in a released copy of the task.
	released := *held
	released.HeldBy = model.HeldByNothing
	registry.Set(&released)

	out, err = srv.humaGetInfo(context.Background(), &struct{}{})
	require.NoError(t, err)
	require.Len(t, out.Body.Tasks, 1)
	assert.Empty(t, out.Body.Tasks[0].HeldBy,
		"the hold is gone from the scheduler, so it has to be gone from /api/daemon too")
}

// TestHumaGetInfo_NoRegistryKeepsTheBootList covers the modes that pass no
// registry: falling back to the boot-time list is right, emptying it is not.
func TestHumaGetInfo_NoRegistryKeepsTheBootList(t *testing.T) {
	srv := &Server{
		stats: newStatsProvider(&model.DaemonInfo{
			Tasks: []model.TaskBrief{{Name: "backup"}},
		}, time.Now()),
	}
	out, err := srv.humaGetInfo(context.Background(), &struct{}{})
	require.NoError(t, err)
	require.Len(t, out.Body.Tasks, 1)
	assert.Equal(t, "backup", out.Body.Tasks[0].Name)
}

func TestHumaGetSystemStats(t *testing.T) {
	srv := &Server{
		stats: newStatsProvider(&model.DaemonInfo{}, time.Now()),
	}
	out, err := srv.humaGetSystemStats(context.Background(), &struct{}{})
	require.NoError(t, err)
	assert.Equal(t, AppName, out.Body.Name)
}

func TestHumaGetMetricsHistory_ReturnsCollectorHistory(t *testing.T) {
	mc := NewMetricsCollector(4)
	// Seed by collecting once — collect() appends a sample synchronously.
	mc.collect()
	srv := &Server{metrics: mc}
	out, err := srv.humaGetMetricsHistory(context.Background(), &struct{}{})
	require.NoError(t, err)
	assert.NotEmpty(t, out.Body)
}

// TestSseDaemonLogHandler_RejectsWhenLimiterFull verifies the early-return
// path when the streamLimiter has no remaining slots: the handler must not
// call the sender at all.
func TestSseDaemonLogHandler_RejectsWhenLimiterFull(t *testing.T) {
	limiter := newStreamLimiter(0, 0) // zero capacity ⇒ acquire always fails
	srv := &Server{streams: limiter}

	called := false
	send := func(_ sse.Message) error {
		called = true
		return nil
	}
	srv.sseDaemonLogHandler(context.Background(), &struct{}{}, send)
	assert.False(t, called, "send must not fire when the limiter rejects the connection")
}

// TestSseDaemonLogHandler_ReturnsEarlyWhenBufferNil exercises the
// daemonLogBuffer-nil branch: the handler still acquires/releases a slot but
// emits no events.
func TestSseDaemonLogHandler_ReturnsEarlyWhenBufferNil(t *testing.T) {
	srv := &Server{
		streams:         newStreamLimiter(2, 2),
		daemonLogBuffer: nil,
	}
	called := false
	send := func(_ sse.Message) error {
		called = true
		return nil
	}
	srv.sseDaemonLogHandler(context.Background(), &struct{}{}, send)
	assert.False(t, called, "send must not fire with nil daemonLogBuffer")
}

// TestSseDaemonLogHandler_ReplaysBufferedLines exercises the happy path: the
// handler must replay buffered lines via send before subscribing to new lines.
// We cancel the context immediately so the subscribe loop exits cleanly.
func TestSseDaemonLogHandler_ReplaysBufferedLines(t *testing.T) {
	buf := NewDaemonLogBuffer(16)
	_, _ = buf.Write([]byte("hello\n"))

	srv := &Server{
		streams:         newStreamLimiter(2, 2),
		daemonLogBuffer: buf,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // ensure the subscribe loop returns immediately after the replay

	var seen []string
	send := func(m sse.Message) error {
		if line, ok := m.Data.(DaemonLogLineEvent); ok {
			seen = append(seen, line.Line)
		}
		return nil
	}
	srv.sseDaemonLogHandler(ctx, &struct{}{}, send)

	require.NotEmpty(t, seen, "expected at least one replayed line")
	assert.Contains(t, seen[0], "hello")
}
