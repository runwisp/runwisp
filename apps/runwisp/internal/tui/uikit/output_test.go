// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package uikit

import (
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
)

func printedRuns(lines ...int64) []model.Run {
	runs := make([]model.Run, len(lines))
	for i, n := range lines {
		runs[i] = model.Run{Status: model.PhaseEnded, StdoutLines: new(n), StderrLines: new(int64(0))}
	}
	return runs
}

func TestUsualOutput_Band(t *testing.T) {
	runs := printedRuns(1, 2, 3, 4, 5, 6, 7, 8, 9, 10)
	runs = append(runs, model.Run{Status: model.PhaseRunning, StdoutLines: new(int64(999)), StderrLines: new(int64(0))})
	b, ok := UsualOutput(runs)
	assert.True(t, ok)
	assert.Equal(t, OutputBand{P10: 1, P90: 9}, b)

	_, ok = UsualOutput(printedRuns(0, 0, 0, 0, 0, 0, 0, 0, 0)) // 9 runs: too few
	assert.False(t, ok)
}

func TestOutputBand_Unusual(t *testing.T) {
	silent := OutputBand{P10: 0, P90: 0}
	assert.True(t, silent.Unusual(3), "a silent task that printed")
	assert.False(t, silent.Unusual(0))

	chatty := OutputBand{P10: 480, P90: 520}
	assert.True(t, chatty.Unusual(3), "far less than usual")
	assert.True(t, chatty.Unusual(5000), "far more than usual")
	assert.False(t, chatty.Unusual(300), "within half of p10")
	assert.False(t, chatty.Unusual(900), "within twice p90")

	quiet := OutputBand{P10: 0, P90: 1}
	assert.False(t, quiet.Unusual(3), "past twice p90 but under the slack")
	assert.True(t, quiet.Unusual(6))
}

func TestFormatLines(t *testing.T) {
	for n, want := range map[int64]string{
		0: "0 lines", 1: "1 line", 37: "37 lines", 1234: "1.2K lines",
		9_960: "10K lines", 48_200: "48K lines", 1_300_000: "1.3M lines",
	} {
		assert.Equal(t, want, FormatLines(n), n)
	}
}
