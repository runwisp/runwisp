// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package execlist

// Column sizing envelopes for the execution table. Each column grows from its
// floor (the narrowest still-useful width) as the pane widens. The four fixed
// columns grow proportionally up to a hard cap of "longest possible value + 2"
// — wide enough that even the longest value keeps two cells of trailing
// breathing room — and never exceed it. The TASK column is flexible: it shares
// the proportional band via taskColIdeal and then absorbs every spare cell once
// the fixed columns have reached their caps.
const (
	// Floors: below these a column truncates rather than shrinking further.
	taskColMin     = 8
	statusColMin   = 8
	startedColMin  = 7
	durationColMin = 7
	triggerColMin  = 6

	// Caps: longest renderable value + 2 cells of padding. A fixed column never
	// grows past its cap; the surplus flows to TASK instead.
	//   STATUS   "daemon_stopped" (14) → 16
	//   STARTED  "Jan 02 15:04"   (12) → 14
	//   DURATION "9999h59m"       (≈8) → 10
	//   TRIGGER  "service"/"startup" (7) → 9
	statusColCap   = 16
	startedColCap  = 14
	durationColCap = 10
	triggerColCap  = 9

	// taskColIdeal is TASK's proportional-band target, not a hard ceiling: TASK
	// keeps every cell of surplus left once the fixed columns hit their caps.
	taskColIdeal = 30

	// Row chrome consumed before/between cells: a 2-space indent plus one
	// space separating each of the five columns.
	colGutter = 2 + 4
)

// colWidths holds the resolved width of every execution-table column for a
// given content width. Field order mirrors the on-screen left-to-right order.
type colWidths struct {
	task     int
	status   int
	started  int
	duration int
	trigger  int
}

// fixedSum returns the combined width of the four fixed columns.
func (c colWidths) fixedSum() int {
	return c.status + c.started + c.duration + c.trigger
}

// computeColWidths distributes contentW across the five columns so the row fills
// the pane exactly without ever overflowing it. Wide panes hand the surplus to
// TASK; as the pane narrows, the fixed columns and TASK give up space together,
// proportionally to how much each can spare, before any cell truncates.
func computeColWidths(contentW int) colWidths {
	avail := contentW - colGutter

	// Per-column [floor, ideal]; index 0 is the flexible TASK column.
	floors := [5]int{taskColMin, statusColMin, startedColMin, durationColMin, triggerColMin}
	ideals := [5]int{taskColIdeal, statusColCap, startedColCap, durationColCap, triggerColCap}

	sumFloor := 0
	for _, f := range floors {
		sumFloor += f
	}

	// Too cramped even for the floors: shrink everything to fit so the row
	// still never renders wider than the pane.
	if avail <= sumFloor {
		w := shrinkToFit(avail, floors)
		return toColWidths(w)
	}

	growCap := 0
	for i := range ideals {
		growCap += ideals[i] - floors[i]
	}

	w := floors
	rem := avail - sumFloor

	if rem >= growCap {
		// Room for every ideal — TASK keeps the remainder.
		w = ideals
		w[0] += rem - growCap
		return toColWidths(w)
	}

	// Transition band: grow each column from its floor toward its ideal in
	// proportion to its remaining headroom; rounding slack lands on TASK.
	distributed := 0
	for i := range w {
		grow := (ideals[i] - floors[i]) * rem / growCap
		w[i] += grow
		distributed += grow
	}
	w[0] += rem - distributed
	return toColWidths(w)
}

// shrinkToFit allocates avail across the columns proportionally to their floors,
// guaranteeing at least one cell each and a total that never exceeds avail.
func shrinkToFit(avail int, floors [5]int) [5]int {
	total := 0
	for _, f := range floors {
		total += f
	}
	var w [5]int
	used := 0
	for i := range floors {
		w[i] = floors[i] * avail / total
		if w[i] < 1 {
			w[i] = 1
		}
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

func toColWidths(w [5]int) colWidths {
	return colWidths{
		task:     w[0],
		status:   w[1],
		started:  w[2],
		duration: w[3],
		trigger:  w[4],
	}
}
