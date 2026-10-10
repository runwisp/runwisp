// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package execlist

// Column sizing envelopes for the execution table. Each column grows from its
// floor (the narrowest still-useful width) as the pane widens. The fixed
// columns grow proportionally up to a hard cap of "longest possible value + 2"
// and never exceed it. On Home the first column is TASK, which shares the
// proportional band via taskColIdeal and then absorbs every spare cell once the
// fixed columns have reached their caps. In a list scoped to one task the
// first column is the absolute start time instead, capped like the others, so
// the table stays compact rather than stretching across the pane.
//
// When the pane is too narrow for every floor, TRIGGER drops first, then
// DURATION, so the remaining columns stay readable instead of every header
// truncating ("DURATI", "TRIGG").
const (
	// Floors: below these a column is dropped (TRIGGER, DURATION) or, for the
	// columns that always show, truncates rather than shrinking further. Each
	// floor fits the column's header and its common values: "just now",
	// "DURATION", "TRIGGER"/"service".
	taskColMin     = 14
	startedColMin  = 8
	durationColMin = 8
	triggerColMin  = 7

	// statusColW fits the longest uikit.StatusLabel ("queue full", 10) plus
	// the badge's own padding.
	statusColW = 12

	// Caps: longest renderable value + 2 cells of padding. A fixed column never
	// grows past its cap; on Home the surplus flows to TASK instead.
	//   STARTED  "Jan _2 15:04"   (12) → 14
	//   DURATION "9999h59m"       (≈8) → 10
	//   TRIGGER  "service"/"startup" (7) → 9
	//   time     "Jan _2 15:04"   (12) → 14, plus " #12" for a multi-instance service
	startedColCap   = 14
	durationColCap  = 10
	triggerColCap   = 9
	timeColW        = 14
	instanceSuffixW = 4

	// taskColIdeal is TASK's proportional-band target, not a hard ceiling: TASK
	// keeps every cell of surplus left once the fixed columns have reached their caps.
	taskColIdeal = 30
)

// colWidths holds the resolved width of every execution-table column for a
// given content width. Field order mirrors the on-screen left-to-right order.
// A zero started, duration or trigger means the column is hidden.
type colWidths struct {
	// scoped marks a list showing one task's runs: the task name would only
	// repeat, so the first column shows each run's absolute start time.
	scoped   bool
	first    int
	status   int
	started  int
	duration int
	trigger  int
}

// rowWidth returns the cells a rendered row occupies: the 2-cell indent, every
// visible column, and one separating space between neighbours.
func (c colWidths) rowWidth() int {
	w := 2 + c.first + 1 + c.status
	for _, col := range []int{c.started, c.duration, c.trigger} {
		if col > 0 {
			w += 1 + col
		}
	}
	return w
}

// gutter is the row chrome for n visible columns: the 2-space indent plus one
// space between each pair of columns.
func gutter(n int) int { return 2 + n - 1 }

// computeColWidths distributes contentW across the columns without ever
// overflowing it. On Home the row fills the pane, with the surplus going to
// TASK. A list scoped to one task passes timeW, the width of its start-time
// column, and keeps its columns compact by padding the last one; timeW 0 means
// the Home table. As the pane narrows the columns give up space together, then
// TRIGGER and DURATION drop once even their floors no longer fit.
func computeColWidths(contentW, timeW int) colWidths {
	if timeW > 0 {
		w := fitColumns(contentW,
			[]int{timeW, statusColW, startedColMin, durationColMin, triggerColMin},
			[]int{timeW, statusColW, startedColCap, durationColCap, triggerColCap})
		// The surplus pads the last column instead, so the table stays compact
		// while a highlighted row still spans the pane.
		if extra := w[0] - timeW; extra > 0 {
			w[0] = timeW
			w[len(w)-1] += extra
		}
		return colWidths{scoped: true, first: w[0], status: w[1], started: w[2], duration: at(w, 3), trigger: at(w, 4)}
	}
	w := fitColumns(contentW,
		[]int{taskColMin, statusColW, startedColMin, durationColMin, triggerColMin},
		[]int{taskColIdeal, statusColW, startedColCap, durationColCap, triggerColCap})
	return colWidths{first: w[0], status: w[1], started: w[2], duration: at(w, 3), trigger: at(w, 4)}
}

// fitColumns drops trailing columns (never the first three) until the floors
// fit, then distributes the width.
func fitColumns(contentW int, floors, ideals []int) []int {
	n := len(floors)
	for n > 3 && contentW-gutter(n) < sum(floors[:n]) {
		n--
	}
	return distribute(contentW-gutter(n), floors[:n], ideals[:n])
}

// at returns w[i], or 0 (hidden) past the visible columns.
func at(w []int, i int) int {
	if i < len(w) {
		return w[i]
	}
	return 0
}

// distribute grows each column from its floor toward its ideal; index 0 is the
// flexible first column, which takes any surplus past the ideals.
func distribute(avail int, floors, ideals []int) []int {
	sumFloor := sum(floors)
	// Too cramped even for the floors: shrink everything to fit so the row
	// still never renders wider than the pane.
	if avail <= sumFloor {
		return shrinkToFit(avail, floors)
	}

	growCap := 0
	for i := range ideals {
		growCap += ideals[i] - floors[i]
	}

	w := append([]int(nil), floors...)
	rem := avail - sumFloor

	if rem >= growCap {
		// Room for every ideal: the first column keeps the remainder.
		copy(w, ideals)
		w[0] += rem - growCap
		return w
	}

	// Transition band: grow each column from its floor toward its ideal in
	// proportion to its remaining headroom; rounding slack lands on the first column.
	distributed := 0
	for i := range w {
		grow := (ideals[i] - floors[i]) * rem / growCap
		w[i] += grow
		distributed += grow
	}
	w[0] += rem - distributed
	return w
}

// shrinkToFit allocates avail across the columns proportionally to their floors,
// guaranteeing at least one cell each and a total that never exceeds avail.
func shrinkToFit(avail int, floors []int) []int {
	total := sum(floors)
	w := make([]int, len(floors))
	used := 0
	for i := range floors {
		w[i] = max(floors[i]*avail/total, 1)
		used += w[i]
	}
	// Trim any overshoot (from per-column rounding/min-1) off the widest column.
	for used > avail {
		widest := 0
		for i := range w {
			if w[i] > w[widest] {
				widest = i
			}
		}
		if w[widest] <= 1 {
			break
		}
		w[widest]--
		used--
	}
	return w
}

func sum(xs []int) int {
	t := 0
	for _, x := range xs {
		t += x
	}
	return t
}
