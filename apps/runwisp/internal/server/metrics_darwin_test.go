// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build darwin

package server

import (
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPopulatePlatformSampleDarwin exercises the real mach/sysctl collector and
// asserts it reports host-level (not Go-heap) numbers — the regression this
// package fixes was macOS falling back to runtime.MemStats with CPU stuck at 0.
func TestPopulatePlatformSampleDarwin(t *testing.T) {
	var s model.MetricsSample
	populatePlatformSample(&s)

	require.Greater(t, s.MemTotal, uint64(1<<30), "MemTotal must be host RAM (> 1 GiB), not the Go heap")
	assert.Positive(t, s.MemUsed, "MemUsed must be non-zero")
	assert.LessOrEqual(t, s.MemUsed, s.MemTotal, "MemUsed cannot exceed MemTotal")
	assert.Greater(t, s.MemUsage, 0.0)
	assert.LessOrEqual(t, s.MemUsage, 100.0)
	assert.GreaterOrEqual(t, s.CPUUsage, 0.0)
	assert.LessOrEqual(t, s.CPUUsage, 100.0)
}
