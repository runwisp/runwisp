// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package executor

import (
	"context"
	"runtime"
	"testing"

	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/procstat"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecutePeakMemoryIgnoresDaemonFloor guards against reporting the
// daemon's own memory as the run's: Linux floors a child's ru_maxrss at the
// parent's RSS high-water mark, so a tiny `sleep` spawned by a 200 MiB process
// would otherwise read as 200 MiB.
func TestExecutePeakMemoryIgnoresDaemonFloor(t *testing.T) {
	ballast := make([]byte, 200<<20)
	for i := range len(ballast) / 4096 {
		ballast[i*4096] = 1
	}

	e := New(Options{LogDir: t.TempDir(), EventBus: events.NewEventBus(), HasLocalTasks: true, Sampler: procstat.New()})
	task := &model.Task{Name: "small", Run: "sleep 0.5"}
	run := &model.Run{ID: ulid.Make().String(), Status: model.PhaseRunning}

	result := e.Execute(context.Background(), task, run)
	runtime.KeepAlive(ballast)

	require.NoError(t, result.Error)
	require.NotNil(t, result.PeakMemoryBytes, "a half-second run is sampled")
	assert.Positive(t, *result.PeakMemoryBytes)
	assert.Less(t, *result.PeakMemoryBytes, int64(50<<20), "peak is the run's, not the daemon's")
	require.NotNil(t, result.CPUTimeMs)
	assert.GreaterOrEqual(t, *result.CPUTimeMs, int64(0))
}
