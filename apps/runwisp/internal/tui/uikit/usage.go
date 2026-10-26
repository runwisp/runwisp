// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package uikit

import (
	"image/color"
	"slices"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

// RunUsage is a finished run's measured usage: CPU time in milliseconds and
// peak memory in bytes.
type RunUsage struct {
	CPUMs    int64
	MemBytes int64
}

// The least a usual run counts as using, so a tiny or zero baseline (a mostly
// idle task's median CPU time is often 0) doesn't hide a run that burns real
// CPU or memory, nor flag 3ms against 1ms. Mirrors the web UI's USAGE_FLOOR.
const (
	UsageFloorCPUMs    = 1000
	UsageFloorMemBytes = 16 << 20
	// UsageFloorDuration is higher than the CPU floor because wall time also
	// carries network and scheduling jitter. Mirrors USAGE_FLOOR.duration.
	UsageFloorDuration = 5 * time.Second
)

// MeasuredUsage reports a run's usage, and false while it is still going or
// when it wasn't measured (non-shell backends, runs too short to sample).
func MeasuredUsage(r *model.Run) (RunUsage, bool) {
	if r.Status != model.PhaseEnded || r.CPUTimeMs == nil || r.PeakMemoryBytes == nil {
		return RunUsage{}, false
	}
	return RunUsage{CPUMs: *r.CPUTimeMs, MemBytes: *r.PeakMemoryBytes}, true
}

// UsualUsage is the median usage over the measured runs given, the baseline a
// heavy run is judged against; false when none was measured. Like the web UI
// it takes the upper median and judges CPU and memory separately.
func UsualUsage(runs []model.Run) (RunUsage, bool) {
	var cpu, mem []int64
	for i := range runs {
		if u, ok := MeasuredUsage(&runs[i]); ok {
			cpu = append(cpu, u.CPUMs)
			mem = append(mem, u.MemBytes)
		}
	}
	if len(cpu) == 0 {
		return RunUsage{}, false
	}
	slices.Sort(cpu)
	slices.Sort(mem)
	return RunUsage{CPUMs: cpu[len(cpu)/2], MemBytes: mem[len(mem)/2]}, true
}

// UsualDuration is the upper median wall time over the finished runs given,
// the baseline a slow run is judged against; false when none has finished.
func UsualDuration(runs []model.Run) (time.Duration, bool) {
	var ds []time.Duration
	for i := range runs {
		if d, ok := runs[i].Duration(); ok && runs[i].Status == model.PhaseEnded {
			ds = append(ds, d)
		}
	}
	if len(ds) == 0 {
		return 0, false
	}
	slices.Sort(ds)
	return ds[len(ds)/2], true
}

// UsageColor colors an amount by how it compares with the task's usual run:
// normal below 1.5× the usual, warning from 1.5×, error from 2×. The usual run
// counts as at least floor, so a task that always uses the same amount stays
// normal however large that amount is.
func UsageColor(value, usual, floor int64, normal color.Color) color.Color {
	ratio := float64(value) / float64(max(usual, floor))
	switch {
	case ratio >= 2:
		return ColorError
	case ratio >= 1.5:
		return ColorWarning
	}
	return normal
}

// FormatCPUTime renders CPU time in milliseconds like a run duration.
func FormatCPUTime(ms int64) string {
	return FormatElapsed(time.Duration(ms) * time.Millisecond)
}
