// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package jitter

import (
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// minGap sorts the resolved starts (phase+offset) and returns the smallest
// spacing between consecutive ones — the quantity Place maximizes within a
// cluster. It also asserts the load-bearing invariant: every offset stays
// inside its window.
func minGap(t *testing.T, windows []Window, offsets map[string]time.Duration) time.Duration {
	t.Helper()
	starts := make([]time.Duration, 0, len(windows))
	for _, w := range windows {
		off, ok := offsets[w.Name]
		require.True(t, ok, "every window must get an offset")
		assert.GreaterOrEqual(t, off, time.Duration(0), "%s offset must be >= 0", w.Name)
		assert.LessOrEqual(t, off, w.Length, "%s offset must stay within its window", w.Name)
		starts = append(starts, w.Phase+off)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	gap := time.Duration(1<<62 - 1)
	for i := 1; i < len(starts); i++ {
		if d := starts[i] - starts[i-1]; d < gap {
			gap = d
		}
	}
	return gap
}

func TestPlace_ThirtyIdenticalWindows(t *testing.T) {
	const n = 30
	at3am := 3 * time.Hour
	windows := make([]Window, 0, n)
	for i := 0; i < n; i++ {
		windows = append(windows, Window{Name: fmt.Sprintf("t%02d", i), Phase: at3am, Length: 30 * time.Minute})
	}
	offsets := Place(windows)

	// 30 windows packed into a 30-minute span → max-min-gap = 30m/29 ≈ 62s,
	// i.e. roughly one task per minute. The even spread falls out for free.
	want := 30 * time.Minute / (n - 1)
	assert.Equal(t, want, minGap(t, windows, offsets),
		"identical windows must spread evenly ~62s apart")

	// The spread should use the whole window: the last task sits at the far end.
	var maxOff time.Duration
	for _, off := range offsets {
		if off > maxOff {
			maxOff = off
		}
	}
	assert.Equal(t, (n-1)*want, maxOff, "the last task should sit at the far end of the window")
}

func TestPlace_OverlappingWindowsLeveled(t *testing.T) {
	windows := []Window{
		{Name: "a", Phase: 3 * time.Hour, Length: 30 * time.Minute},                // [03:00, 03:30]
		{Name: "b", Phase: 3*time.Hour + 10*time.Minute, Length: 30 * time.Minute}, // [03:10, 03:40]
	}
	offsets := Place(windows)

	// Two tasks: maximum separation puts a at its earliest (03:00) and b at its
	// latest (03:40) → 40m apart, both still inside their windows.
	assert.Equal(t, 40*time.Minute, minGap(t, windows, offsets))
	assert.Equal(t, time.Duration(0), offsets["a"])
	assert.Equal(t, 30*time.Minute, offsets["b"])
}

func TestPlace_SingleWindow(t *testing.T) {
	offsets := Place([]Window{{Name: "solo", Phase: 3 * time.Hour, Length: 30 * time.Minute}})
	assert.Equal(t, map[string]time.Duration{"solo": 0}, offsets)
}

func TestPlace_Empty(t *testing.T) {
	assert.Empty(t, Place(nil))
}

func TestPlace_ZeroLengthWindowsDoNotCrash(t *testing.T) {
	// Windows too tight to spread (length 0): everyone pins to their phase, no
	// crash, all offsets zero.
	windows := []Window{
		{Name: "a", Phase: 3 * time.Hour, Length: 0},
		{Name: "b", Phase: 3 * time.Hour, Length: 0},
	}
	offsets := Place(windows)
	assert.Equal(t, time.Duration(0), offsets["a"])
	assert.Equal(t, time.Duration(0), offsets["b"])
}

func TestPlace_TightButNonzeroWindow(t *testing.T) {
	// Three tasks sharing a 2-second window → 1s apart, all within the window.
	windows := []Window{
		{Name: "a", Phase: time.Hour, Length: 2 * time.Second},
		{Name: "b", Phase: time.Hour, Length: 2 * time.Second},
		{Name: "c", Phase: time.Hour, Length: 2 * time.Second},
	}
	offsets := Place(windows)
	assert.Equal(t, time.Second, minGap(t, windows, offsets))
}

func TestPlace_Deterministic(t *testing.T) {
	windows := []Window{
		{Name: "c", Phase: 3 * time.Hour, Length: 30 * time.Minute},
		{Name: "a", Phase: 3 * time.Hour, Length: 30 * time.Minute},
		{Name: "b", Phase: 3*time.Hour + 5*time.Minute, Length: 10 * time.Minute},
	}
	first := Place(windows)
	for i := 0; i < 5; i++ {
		assert.Equal(t, first, Place(windows), "Place must be a pure function of its input")
	}
	// Input order must not matter: shuffling windows yields the same mapping.
	shuffled := []Window{windows[2], windows[0], windows[1]}
	assert.Equal(t, first, Place(shuffled), "offset mapping must not depend on input order")
}

// TestPlace_PerClusterIndependence pins the cluster fix: a tight (zero-length)
// group must not compress an unrelated loose group's spread. The two 03:00
// tasks share a 30m window with nothing else overlapping it, so they land at
// the window's two ends (0 and 30m) regardless of the pinned 04:00 pair. Under
// the old single global gap the pinned pair would force g=0 and flatten the
// loose pair to 0,0 — this asserts they still spread.
func TestPlace_PerClusterIndependence(t *testing.T) {
	windows := []Window{
		{Name: "loose-a", Phase: 3 * time.Hour, Length: 30 * time.Minute},
		{Name: "loose-b", Phase: 3 * time.Hour, Length: 30 * time.Minute},
		{Name: "tight-a", Phase: 4 * time.Hour, Length: 0},
		{Name: "tight-b", Phase: 4 * time.Hour, Length: 0},
	}
	offsets := Place(windows)

	assert.Equal(t, time.Duration(0), offsets["loose-a"], "first loose task sits at its window start")
	assert.Equal(t, 30*time.Minute, offsets["loose-b"], "second loose task reaches the far end despite the tight cluster")
	assert.Equal(t, time.Duration(0), offsets["tight-a"])
	assert.Equal(t, time.Duration(0), offsets["tight-b"], "pinned (zero-length) windows can only sit at their phase")
}

// TestPlace_EDFTiebreak proves the earliest-deadline-first tiebreak: when two
// windows share a phase, the tighter one (earlier deadline) takes the earliest
// slot — even when its name would sort later. "z-tight" closes at 03:10 while
// "a-wide" closes at 03:30, so z-tight wins offset 0 and a-wide is pushed to
// the far end. A plain name sort would have handed a-wide the early slot.
func TestPlace_EDFTiebreak(t *testing.T) {
	windows := []Window{
		{Name: "a-wide", Phase: 3 * time.Hour, Length: 30 * time.Minute},
		{Name: "z-tight", Phase: 3 * time.Hour, Length: 10 * time.Minute},
	}
	offsets := Place(windows)

	assert.Equal(t, time.Duration(0), offsets["z-tight"], "the tightest window takes the earliest slot")
	assert.Equal(t, 30*time.Minute, offsets["a-wide"])
	assert.Equal(t, 30*time.Minute, minGap(t, windows, offsets))
}

func TestPlace_StaggeredPhasesStayWithinWindows(t *testing.T) {
	// A spread of phases and lengths; the invariant we always hold is that every
	// offset lands inside its window (asserted inside minGap).
	windows := []Window{
		{Name: "early", Phase: 1 * time.Hour, Length: 15 * time.Minute},
		{Name: "mid1", Phase: 3 * time.Hour, Length: 30 * time.Minute},
		{Name: "mid2", Phase: 3*time.Hour + 20*time.Minute, Length: 20 * time.Minute},
		{Name: "late", Phase: 23 * time.Hour, Length: 30 * time.Minute},
	}
	offsets := Place(windows)
	require.Len(t, offsets, len(windows))
	assert.Positive(t, minGap(t, windows, offsets), "distinct phases must stay distinct")
}
