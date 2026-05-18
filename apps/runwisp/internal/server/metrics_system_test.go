// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"runtime"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

// TestPopulateFallbackSample asserts populateFallbackSample sets MemTotal/MemUsed
// from runtime.MemStats and leaves CPUUsage / MemUsage untouched (these are not
// observable from MemStats alone, so the fallback intentionally skips them).
func TestPopulateFallbackSample(t *testing.T) {
	s := model.MetricsSample{
		CPUUsage: 42.0,
		MemUsage: 17.0,
	}
	populateFallbackSample(&s)

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	assert.Equal(t, m.Sys, s.MemTotal, "MemTotal must come from runtime.MemStats.Sys")
	assert.Equal(t, m.Alloc, s.MemUsed, "MemUsed must come from runtime.MemStats.Alloc")
	assert.InDelta(t, 42.0, s.CPUUsage, 0.001, "CPUUsage must be left untouched by the fallback")
	assert.InDelta(t, 17.0, s.MemUsage, 0.001, "MemUsage must be left untouched by the fallback")
}

// TestPopulateFallbackStats asserts populateFallbackStats overwrites all four
// of the destination's stats fields — including CPUUsage / MemUsage — from a
// fresh sample (so any pre-populated CPU values get wiped).
func TestPopulateFallbackStats(t *testing.T) {
	stats := model.SystemStats{
		CPUUsage: 99.0, // must be overwritten back to 0
		MemUsage: 88.0, // ditto
	}
	populateFallbackStats(&stats)

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	assert.Equal(t, m.Sys, stats.MemTotal)
	assert.Equal(t, m.Alloc, stats.MemUsed)
	assert.InDelta(t, 0.0, stats.CPUUsage, 0.001,
		"fallback wipes CPUUsage because runtime.MemStats can't report it")
	assert.InDelta(t, 0.0, stats.MemUsage, 0.001,
		"fallback wipes MemUsage for the same reason")
}
