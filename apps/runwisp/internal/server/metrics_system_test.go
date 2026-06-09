// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"runtime"
	"testing"

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
