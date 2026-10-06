// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package procstat

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/runwisp/runwisp/internal/model"
)

// clock is a hand-driven time source. testutil.Clock can't be used here:
// testutil imports executor, which imports this package.
type clock struct{ now time.Time }

func newClock() *clock                   { return &clock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)} }
func (c *clock) Now() time.Time          { return c.now }
func (c *clock) Advance(d time.Duration) { c.now = c.now.Add(d) }

// fakeReader serves whatever the test last put in stats.
type fakeReader struct{ stats map[int]groupStat }

func (f *fakeReader) read(map[int]struct{}) map[int]groupStat { return f.stats }

// newTestSampler parks the background loop on a long sleep so the test drives
// every sample by hand.
func newTestSampler(clk *clock) (*Sampler, *fakeReader) {
	fr := &fakeReader{}
	return newSampler(fr.read, clk.Now, time.Hour), fr
}

func TestSamplerCPUPercentAndPeak(t *testing.T) {
	clk := newClock()
	s, fr := newTestSampler(clk)
	stop := s.Track("backup", "run1", 100)

	fr.stats = map[int]groupStat{100: {CPU: time.Second, RSS: 50 << 20, Peak: 80 << 20}}
	require.True(t, s.sampleOnce())
	// First reading has no delta: memory only.
	assert.Equal(t, map[string]model.ResourceUsage{"backup": {CPUPercent: 0, MemoryBytes: 50 << 20}}, s.TaskUsage())

	clk.Advance(2 * time.Second)
	fr.stats = map[int]groupStat{100: {CPU: 2 * time.Second, RSS: 10 << 20, Peak: 10 << 20}}
	s.sampleOnce()
	assert.Equal(t, map[string]model.ResourceUsage{"backup": {CPUPercent: 50, MemoryBytes: 10 << 20}}, s.TaskUsage())

	peak, ok := stop()
	assert.True(t, ok)
	assert.Equal(t, int64(80<<20), peak, "peak survives usage dropping")
	assert.Empty(t, s.TaskUsage())
	assert.False(t, s.sampleOnce(), "loop exits once nothing is tracked")
}

func TestSamplerSumsRunsPerTask(t *testing.T) {
	clk := newClock()
	s, fr := newTestSampler(clk)
	s.Track("web", "a", 1)
	s.Track("web", "b", 2)
	s.Track("idle", "c", 3)
	fr.stats = map[int]groupStat{1: {RSS: 1}, 2: {RSS: 2}}
	s.sampleOnce()
	assert.Equal(t, map[string]model.ResourceUsage{"web": {MemoryBytes: 3}}, s.TaskUsage(),
		"a run with no live process is left out")
	assert.Equal(t, map[string]model.ResourceUsage{"a": {MemoryBytes: 1}, "b": {MemoryBytes: 2}}, s.RunUsage())
}

func TestSamplerUnsampledRunHasNoPeak(t *testing.T) {
	clk := newClock()
	s, _ := newTestSampler(clk)
	_, ok := s.Track("t", "r", 1)()
	assert.False(t, ok)
}

func TestSamplerIntervalIsFastForYoungRuns(t *testing.T) {
	clk := newClock()
	s := newSampler(func(map[int]struct{}) map[int]groupStat { return nil }, clk.Now, 100*time.Millisecond)
	s.Track("t", "r", 1)
	assert.Equal(t, 100*time.Millisecond, s.interval())
	clk.Advance(youngFor)
	assert.Equal(t, slowInterval, s.interval())
}

func TestParsePS(t *testing.T) {
	out := []byte(`  100  2048  0:01.50
  100  1024  1:00:00.00
  200  4096  0:00.00
 junk line here please
`)
	got := parsePS(out, map[int]struct{}{100: {}})
	assert.Equal(t, map[int]groupStat{100: {
		CPU:  time.Hour + 1500*time.Millisecond,
		RSS:  3 << 20,
		Peak: 3 << 20,
	}}, got)
}

func TestParsePSTime(t *testing.T) {
	cases := map[string]time.Duration{
		"0:00.05":    50 * time.Millisecond,
		"12:34":      12*time.Minute + 34*time.Second,
		"01:02:03":   time.Hour + 2*time.Minute + 3*time.Second,
		"2-01:00:00": 49 * time.Hour,
		"1:02:03.50": time.Hour + 2*time.Minute + 3500*time.Millisecond,
	}
	for in, want := range cases {
		got, err := parsePSTime(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	_, err := parsePSTime("x:00")
	assert.Error(t, err)
}
