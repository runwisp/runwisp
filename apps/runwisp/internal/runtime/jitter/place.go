// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package jitter computes deterministic per-task start slots within their
// jitter windows. It is pure geometry — no cron, no clock, no state — so
// placement is exhaustively unit-testable and obeys the determinism invariant:
// the same windows always yield the same slots.
//
// Each task gets a slot offset inside its own window. The slot is the *latest*
// a start may slip (its deadline); the runtime gate pulls runs forward when the
// box is idle, so the slots double as a release order under congestion rather
// than as fixed delays. Two ideas shape the offsets:
//
//   - Even spread (max-min-gap): within a set of contending windows, slots are
//     chosen so the smallest spacing between any two consecutive ones is as
//     large as possible. For N tasks sharing one window this is a 1/(N-1)
//     spread. The stagger is what makes a congested backlog release evenly
//     instead of bursting at the window edge.
//   - Per-cluster independence: windows are grouped into clusters of
//     chain-overlapping [Phase, Phase+Length] ranges and spread one cluster at
//     a time. Tasks whose windows don't overlap never compress each other's
//     spread, and non-contending tasks get offset 0.
//
// Ties resolve earliest-deadline-first: among windows that could take the same
// slot, the tightest one (smallest Length) wins it. Because the slots also feed
// the gate's release order, EDF ordering falls out for free.
package jitter

import (
	"sort"
	"time"
)

// Window is one task's placement constraint. Phase is the task's next fire
// reduced to a 24-hour time-of-day dial (base mod 24h); Length is the spread
// window, so the start may land anywhere in [Phase, Phase+Length]. Name breaks
// ties deterministically and keys the returned offset map.
type Window struct {
	Name   string
	Phase  time.Duration
	Length time.Duration
}

// Place returns each window's slot offset within [0, Length]. Windows are
// grouped into clusters of chain-overlapping [Phase, Phase+Length] ranges and
// each cluster is spread independently to maximize the minimum spacing between
// consecutive starts; a lone window (or none) yields offset 0. The result is
// keyed by Window.Name and is a deterministic function of the input — input
// order does not matter.
func Place(windows []Window) map[string]time.Duration {
	out := make(map[string]time.Duration, len(windows))
	if len(windows) == 0 {
		return out
	}

	ws := make([]Window, len(windows))
	copy(ws, windows)
	// Earliest-deadline-first ordering: phase, then the tighter window, then
	// name. The greedy left-pack below honours this order, so the tightest
	// window takes the earliest slot.
	sort.Slice(ws, func(i, j int) bool {
		if ws[i].Phase != ws[j].Phase {
			return ws[i].Phase < ws[j].Phase
		}
		if ws[i].Length != ws[j].Length {
			return ws[i].Length < ws[j].Length
		}
		return ws[i].Name < ws[j].Name
	})

	for _, cluster := range clusters(ws) {
		spread(cluster, out)
	}
	return out
}

// clusters splits phase-sorted windows into chains of overlapping ranges. A
// window joins the running cluster while its Phase is at or before the
// cluster's furthest end so far; a gap starts a new cluster. Spreading each
// cluster on its own keeps a tight window in one group from compressing the
// spread of an unrelated group.
func clusters(ws []Window) [][]Window {
	var groups [][]Window
	cur := []Window{ws[0]}
	end := ws[0].Phase + ws[0].Length
	for _, w := range ws[1:] {
		if w.Phase > end {
			groups = append(groups, cur)
			cur = []Window{w}
			end = w.Phase + w.Length
			continue
		}
		cur = append(cur, w)
		if e := w.Phase + w.Length; e > end {
			end = e
		}
	}
	return append(groups, cur)
}

// spread places one cluster (phase-sorted, EDF-ordered) and writes each
// window's offset into out. A single-window cluster sits at its phase; larger
// clusters binary-search the largest gap g for which a left-packed greedy
// placement fits every window. g=0 is always feasible (everyone at their
// phase); the cluster's extent is an upper bound no gap can exceed.
func spread(ws []Window, out map[string]time.Duration) {
	if len(ws) == 1 {
		out[ws[0].Name] = 0
		return
	}

	minPhase := ws[0].Phase // phase-sorted, so the first is the earliest
	var maxEnd time.Duration
	for _, w := range ws {
		if end := w.Phase + w.Length; end > maxEnd {
			maxEnd = end
		}
	}
	lo, hi := time.Duration(0), maxEnd-minPhase
	for lo < hi {
		mid := lo + (hi-lo+1)/2
		if _, ok := greedy(ws, mid); ok {
			lo = mid
		} else {
			hi = mid - 1
		}
	}

	starts, _ := greedy(ws, lo)
	for i, w := range ws {
		out[w.Name] = starts[i] - w.Phase
	}
}

// greedy places windows left-to-right (sorted by phase), putting each at the
// earliest point that is both inside its window and at least gap past the
// previous start. It reports infeasible the moment a window can't satisfy both.
// A suboptimal ordering can only make the search settle on a smaller gap, never
// produce a start outside a window — so the returned placement is always valid.
func greedy(ws []Window, gap time.Duration) ([]time.Duration, bool) {
	starts := make([]time.Duration, len(ws))
	var prev time.Duration
	for i, w := range ws {
		t := w.Phase
		if i > 0 && prev+gap > t {
			t = prev + gap
		}
		if t > w.Phase+w.Length {
			return nil, false
		}
		starts[i] = t
		prev = t
	}
	return starts, true
}
