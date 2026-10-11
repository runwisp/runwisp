// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package uikit

import (
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

func measuredRun(phase model.RunPhase, cpuMs, mem int64) model.Run {
	return model.Run{Status: phase, CPUTimeMs: &cpuMs, PeakMemoryBytes: &mem}
}

func TestUsualUsage_UpperMedianOverMeasuredEndedRuns(t *testing.T) {
	runs := []model.Run{
		measuredRun(model.PhaseEnded, 300, 30),
		measuredRun(model.PhaseEnded, 100, 10),
		measuredRun(model.PhaseRunning, 9000, 9000), // still going: skipped
		{Status: model.PhaseEnded},                  // not measured: skipped
		measuredRun(model.PhaseEnded, 200, 40),
		measuredRun(model.PhaseEnded, 400, 20),
	}
	u, ok := UsualUsage(runs)
	assert.True(t, ok)
	// CPU and memory sort independently; with four values the upper median wins.
	assert.Equal(t, RunUsage{CPUMs: 300, MemBytes: 30}, u)

	_, ok = UsualUsage([]model.Run{{Status: model.PhaseEnded}})
	assert.False(t, ok)
}

func TestUsageColor_Thresholds(t *testing.T) {
	const floor = UsageFloorCPUMs
	assert.Equal(t, ColorText, UsageColor(14_000, 10_000, floor, ColorText))
	assert.Equal(t, ColorWarning, UsageColor(15_000, 10_000, floor, ColorText))
	assert.Equal(t, ColorError, UsageColor(20_000, 10_000, floor, ColorText))
	// A zero usual counts as the floor: 900ms is no outlier, 2s is.
	assert.Equal(t, ColorText, UsageColor(900, 0, floor, ColorText))
	assert.Equal(t, ColorError, UsageColor(2000, 0, floor, ColorText))
}

func TestFormatCPUTime(t *testing.T) {
	assert.Equal(t, "250ms", FormatCPUTime(250))
	assert.Equal(t, "1m 52s", FormatCPUTime(112_000))
}
