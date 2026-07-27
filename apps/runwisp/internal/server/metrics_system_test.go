// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"runtime"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

// TestPopulateFallbackSample asserts populateFallbackSampleFromMemStats sets
// MemTotal/MemUsed from the injected runtime.MemStats and leaves CPUUsage /
// MemUsage untouched (these are not observable from MemStats alone, so the
// fallback intentionally skips them).
func TestPopulateFallbackSample(t *testing.T) {
	s := model.MetricsSample{
		CPUUsage: 42.0,
		MemUsage: 17.0,
	}
	m := runtime.MemStats{Sys: 1024, Alloc: 512}
	populateFallbackSampleFromMemStats(&s, &m)

	assert.Equal(t, uint64(1024), s.MemTotal, "MemTotal must come from MemStats.Sys")
	assert.Equal(t, uint64(512), s.MemUsed, "MemUsed must come from MemStats.Alloc")
	assert.InDelta(t, 42.0, s.CPUUsage, 0.001, "CPUUsage must be left untouched by the fallback")
	assert.InDelta(t, 17.0, s.MemUsage, 0.001, "MemUsage must be left untouched by the fallback")
}

// TestPopulateFallbackSample_RealRuntime exercises the wrapper that reads
// real runtime.MemStats — values are non-deterministic, but we can still
// assert MemTotal/MemUsed end up non-zero.
func TestPopulateFallbackSample_RealRuntime(t *testing.T) {
	var s model.MetricsSample
	populateFallbackSample(&s)
	assert.Greater(t, s.MemTotal, uint64(0))
	assert.Greater(t, s.MemUsed, uint64(0))
}

// TestMetricsCollector_HistoryAfterCollect asserts collect() appends a single
// sample and History() returns a snapshot copy.
func TestMetricsCollector_HistoryAfterCollect(t *testing.T) {
	mc := NewMetricsCollector(3)
	mc.collect()
	mc.collect()

	hist := mc.History()
	assert.Len(t, hist, 2)

	// Mutating the returned slice must not affect the collector's storage.
	hist[0] = model.MetricsSample{}
	assert.NotEqual(t, model.MetricsSample{}, mc.History()[0])
}

// TestMetricsCollector_CapsAtMaxSize asserts the ring drops oldest when full.
func TestMetricsCollector_CapsAtMaxSize(t *testing.T) {
	mc := NewMetricsCollector(2)
	for range 5 {
		mc.collect()
	}
	assert.Len(t, mc.History(), 2)
}

// TestMetricsCollector_OnSampleFires asserts each collect() invokes the
// onSample hook with the freshly stored sample — the seam the server uses to
// fan samples out over the event bus.
func TestMetricsCollector_OnSampleFires(t *testing.T) {
	mc := NewMetricsCollector(4)
	var got int
	mc.onSample = func(model.MetricsSample) { got++ }
	mc.collect()
	mc.collect()
	assert.Equal(t, 2, got)
}

// TestBroadcastSample asserts the metrics callback publishes a system event for
// every sample but only emits a config.stale event when staleness flips —
// exactly the behaviour that lets the UI drop its /api/system + /api/info
// polling.
func TestBroadcastSample(t *testing.T) {
	bus := events.NewEventBus()
	stale := false
	srv := &Server{
		eventBus:    bus,
		stats:       newStatsProvider(nil, time.Now()),
		configStale: func() bool { return stale },
	}
	srv.configStaleLast = srv.currentConfigStale() // seed baseline like Start()

	var systemCount, staleCount int
	var lastStale bool
	bus.Subscribe(events.EventSystemSample, func(e events.Event) {
		systemCount++
		s, ok := e.Data.(events.SystemSampleEvent)
		assert.True(t, ok, "system event carries a SystemSampleEvent payload")
		assert.NotEmpty(t, s.Uptime, "uptime is formatted server-side")
	})
	bus.Subscribe(events.EventConfigStale, func(e events.Event) {
		staleCount++
		if cs, ok := e.Data.(events.ConfigStaleEvent); ok {
			lastStale = cs.Stale
		}
	})

	srv.broadcastSample(model.MetricsSample{CPUUsage: 10})
	srv.broadcastSample(model.MetricsSample{CPUUsage: 20})
	assert.Equal(t, 2, systemCount, "every sample publishes a system event")
	assert.Equal(t, 0, staleCount, "no config event while staleness is unchanged")

	stale = true
	srv.broadcastSample(model.MetricsSample{CPUUsage: 30})
	assert.Equal(t, 1, staleCount, "a flip publishes exactly one config event")
	assert.True(t, lastStale)

	srv.broadcastSample(model.MetricsSample{CPUUsage: 40})
	assert.Equal(t, 1, staleCount, "no further event while staleness stays true")

	stale = false
	srv.broadcastSample(model.MetricsSample{CPUUsage: 50})
	assert.Equal(t, 2, staleCount, "flipping back publishes again")
	assert.False(t, lastStale)
}

// TestPopulateFallbackStats asserts populateFallbackStatsFromMemStats overwrites
// all four of the destination's stats fields — including CPUUsage / MemUsage —
// from a fresh sample (so any pre-populated CPU values get wiped).
func TestPopulateFallbackStats(t *testing.T) {
	stats := model.SystemStats{
		CPUUsage: 99.0, // must be overwritten back to 0
		MemUsage: 88.0, // ditto
	}
	m := runtime.MemStats{Sys: 2048, Alloc: 1024}
	populateFallbackStatsFromMemStats(&stats, &m)

	assert.Equal(t, uint64(2048), stats.MemTotal)
	assert.Equal(t, uint64(1024), stats.MemUsed)
	assert.InDelta(t, 0.0, stats.CPUUsage, 0.001,
		"fallback wipes CPUUsage because runtime.MemStats can't report it")
	assert.InDelta(t, 0.0, stats.MemUsage, 0.001,
		"fallback wipes MemUsage for the same reason")
}
