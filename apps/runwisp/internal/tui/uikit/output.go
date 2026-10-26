// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package uikit

import (
	"fmt"
	"slices"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

// OutputBand is the range of output a task's runs usually print, in lines:
// its 10th and 90th percentile. Mirrors the web UI's outputBand.
type OutputBand struct {
	P10, P90 int64
}

const (
	// outputBandMinRuns is how many finished runs a task needs before a run is
	// judged against its band; fewer say little about what is usual.
	outputBandMinRuns = 10
	// outputBandSlack is the fewest lines a run must be past the band's edge,
	// so 3 lines against a usual 1 isn't flagged.
	outputBandSlack = 5
)

// UsualOutput is the output band over the finished runs given; false when
// fewer than outputBandMinRuns have line counts.
func UsualOutput(runs []model.Run) (OutputBand, bool) {
	var lines []int64
	for i := range runs {
		if n, ok := runs[i].OutputLines(); ok {
			lines = append(lines, n)
		}
	}
	if len(lines) < outputBandMinRuns {
		return OutputBand{}, false
	}
	slices.Sort(lines)
	return OutputBand{P10: nearestRank(lines, 10), P90: nearestRank(lines, 90)}, true
}

// nearestRank is the p-th percentile of sorted by the nearest-rank method.
func nearestRank(sorted []int64, p int) int64 {
	rank := (p*len(sorted) + 99) / 100 // ceil(p/100 · n), 1-based
	return sorted[max(rank, 1)-1]
}

// Unusual reports whether a run's line count is clearly outside the band:
// more than twice its 90th percentile or under half its 10th, and at least
// outputBandSlack lines past it. A task that is silent nearly every time is
// flagged by any output at all.
func (b OutputBand) Unusual(lines int64) bool {
	if b.P90 == 0 {
		return lines > 0
	}
	high := lines > 2*b.P90 && lines-b.P90 >= outputBandSlack
	low := 2*lines < b.P10 && b.P10-lines >= outputBandSlack
	return high || low
}

// FormatLines renders a line count for a column: "1 line", "37 lines",
// "1.2K lines", "1.3M lines".
func FormatLines(n int64) string {
	switch {
	case n == 1:
		return "1 line"
	case n < 1000:
		return fmt.Sprintf("%d lines", n)
	case n < 9_950: // 9_950+ would round to "10.0K"
		return fmt.Sprintf("%.1fK lines", float64(n)/1000)
	case n < 999_500:
		return fmt.Sprintf("%dK lines", (n+500)/1000)
	default:
		return fmt.Sprintf("%.1fM lines", float64(n)/1_000_000)
	}
}
