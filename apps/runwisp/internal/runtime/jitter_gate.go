// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"cmp"
	"slices"
	"sync"
	"time"
)

// stopper is the slice of *time.Timer the gate relies on: the ability to
// cancel a not-yet-fired breach. Tests inject a fake to fire breaches
// deterministically instead of waiting on the wall clock.
type stopper interface {
	Stop() bool
}

// afterFunc schedules fn after d and returns a handle to cancel it. Production
// wires time.AfterFunc; gate tests substitute a controllable fake.
type afterFunc func(d time.Duration, fn func()) stopper

// heldRun is one submitted-but-not-yet-triggered jittered fire. slot is the
// deadline — the latest the start may slip — and doubles as the release order
// when the gate frees. horizon is the task's window length: an in-flight
// jittered run older than this no longer blocks the task.
type heldRun struct {
	taskName string
	tick     time.Time     // cron tick, backdated onto the run's CreatedAt
	slot     time.Time     // deadline = tick + spread offset
	horizon  time.Duration // window length for the "free for this task" check
	timer    stopper       // breach timer; nil once triggered
}

// jitterGate is a daemon-wide, work-conserving gate that targets one in-flight
// jittered run at a time. A submitted fire runs at min(when the gate frees for
// it, its slot): pulled forward and run back-to-back when the box is idle (no
// wasted delay), or released on its staggered slot when the gate is congested
// (so a backlog drains spread-out instead of bursting). Only jittered cron runs
// participate — services and plain tasks never gate.
//
// It is owned by defaultTaskManager and reuses its clock and run-trigger path.
// The gate holds its own mutex; the lock order is always gateMu → manager mu
// (trigger re-acquires the manager lock), never the reverse — onComplete is
// invoked only after the manager lock is released. trigger may also take the
// scheduler's lock (its pause check), which is a leaf: gateMu → scheduler mutex.
type jitterGate struct {
	now     func() time.Time
	after   afterFunc
	trigger func(taskName string, tick time.Time) (runID string, started bool)

	mu       sync.Mutex
	pending  []*heldRun         // ordered by slot then name (EDF)
	inflight map[string]gateRun // runID → gate-triggered run
	closed   bool
}

// gateRun is one in-flight gate-triggered run. slot is the deadline it held;
// while start is before it the run was pulled forward and still owns that
// later deadline, which a same-tick peer can take over (see swapSlot).
type gateRun struct {
	start time.Time
	slot  time.Time
}

func newJitterGate(now func() time.Time, trigger func(string, time.Time) (string, bool)) *jitterGate {
	return &jitterGate{
		now:      now,
		after:    func(d time.Duration, fn func()) stopper { return time.AfterFunc(d, fn) },
		trigger:  trigger,
		inflight: make(map[string]gateRun),
	}
}

// submit enqueues a jittered fire and either pulls it forward immediately (the
// gate is free) or holds it with a breach timer armed to its slot. tick is the
// cron tick (for CreatedAt backdating), slot the deadline, window the free
// check horizon.
func (g *jitterGate) submit(taskName string, tick, slot time.Time, window time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return
	}

	h := &heldRun{taskName: taskName, tick: tick, slot: slot, horizon: window}
	if !g.freeFor(window) {
		g.swapSlot(h)
	}
	g.insert(h)

	// Arm the breach timer before advancing: if advance pulls h (or an
	// earlier-slot peer) forward, it stops the timer; if not, the timer
	// releases h at its deadline. A delay of 0 (offset-0 task that can't be
	// pulled forward) breaches almost immediately — its deadline is the tick.
	delay := max(h.slot.Sub(g.now()), 0)
	h.timer = g.after(delay, func() { g.breach(taskName) })

	g.advance()
}

// swapSlot fixes the arrival race between fires of the same tick. The cron
// loop hands them to the gate in no particular order, so a later-slot fire can
// find the gate idle and start first; an earlier-slot peer arriving after it
// would then find its own deadline already due and breach at once, putting two
// runs on the box at the tick. Had the peer arrived first, EDF would have run
// it first and held the other until its slot. So the peer takes over the
// deadline the pulled-forward run no longer needs (capped at its own window),
// and hands it its own earlier one: the tick's deadlines stay spread across
// the window, each used once. A fire from a later tick may take one over too;
// it still starts within its own window, just less likely beside the run.
// Assumes the lock is held.
func (g *jitterGate) swapSlot(h *heldRun) {
	id := ""
	for rid, r := range g.inflight {
		if r.start.Before(r.slot) && r.slot.After(h.slot) &&
			(id == "" || r.slot.After(g.inflight[id].slot)) {
			id = rid
		}
	}
	if id == "" {
		return
	}
	r := g.inflight[id]
	r.slot, h.slot = h.slot, minTime(r.slot, h.tick.Add(h.horizon))
	g.inflight[id] = r
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// cmpHeld orders held runs by slot deadline, then name — the EDF release order.
func cmpHeld(a, b *heldRun) int {
	return cmp.Or(a.slot.Compare(b.slot), cmp.Compare(a.taskName, b.taskName))
}

// insert places h into pending keeping the slot-then-name order. Assumes the
// lock is held.
func (g *jitterGate) insert(h *heldRun) {
	i, _ := slices.BinarySearchFunc(g.pending, h, cmpHeld)
	g.pending = slices.Insert(g.pending, i, h)
}

// advance pulls forward every pending fire the gate is currently free for,
// earliest slot first. Each trigger marks the gate occupied (a fresh run blocks
// any task whose horizon is positive), so under steady state this releases one
// run per drained gate; an overrun that has aged past a waiter's horizon frees
// that waiter too. Assumes the lock is held.
func (g *jitterGate) advance() {
	for len(g.pending) > 0 {
		h := g.pending[0]
		if !g.freeFor(h.horizon) {
			return
		}
		g.pending = g.pending[1:]
		if h.timer != nil {
			h.timer.Stop()
		}
		g.fire(h)
	}
}

// breach releases a held fire at its slot deadline even though the gate is
// congested — it may then run concurrently with the in-flight run. A no-op if
// the fire was already pulled forward (removed from pending) or the gate is
// closed; the "still pending" check guards against a Stop() that lost the race
// with a fired timer. Acquires the lock.
func (g *jitterGate) breach(taskName string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return
	}
	h, ok := g.remove(taskName)
	if !ok {
		return
	}
	g.fire(h)
}

// fire triggers the run and, if it actually started (or queued), records it as
// in-flight so it blocks peers until it completes. A refused trigger (shutdown)
// or an immediately-rejected run is not tracked — it never reaches onComplete.
// Assumes the lock is held; trigger re-acquires the manager lock beneath it.
func (g *jitterGate) fire(h *heldRun) {
	runID, started := g.trigger(h.taskName, h.tick)
	if !started {
		return
	}
	g.inflight[runID] = gateRun{start: g.now(), slot: h.slot}
}

// freeFor reports whether no in-flight jittered run started within the given
// horizon — i.e. every gate run either finished or has overrun this task's
// window and so no longer blocks it. Assumes the lock is held.
func (g *jitterGate) freeFor(horizon time.Duration) bool {
	now := g.now()
	for _, r := range g.inflight {
		if now.Sub(r.start) < horizon {
			return false
		}
	}
	return true
}

// remove drops the pending entry for taskName and stops its breach timer,
// reporting whether it was found. Assumes the lock is held.
func (g *jitterGate) remove(taskName string) (*heldRun, bool) {
	for i, h := range g.pending {
		if h.taskName == taskName {
			g.pending = slices.Delete(g.pending, i, i+1)
			if h.timer != nil {
				h.timer.Stop()
			}
			return h, true
		}
	}
	return nil, false
}

// onComplete retires a finished run from the in-flight set and, once no gate
// run remains, pulls the earliest-slot pending fire forward. A no-op for runs
// the gate never triggered (services, plain tasks, manual/API runs of a
// jittered task), so it can be called for every completion. Acquires the lock;
// the manager lock must already be released to preserve the gateMu → mu order.
func (g *jitterGate) onComplete(runID string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.inflight[runID]; !ok {
		return
	}
	delete(g.inflight, runID)
	if len(g.inflight) == 0 {
		g.advance()
	}
}

// shutdown abandons every pending fire and stops its timer so no held task ever
// starts a run after the daemon begins shutting down. In-flight runs are
// cancelled by the manager's own shutdown pass; with pending cleared, their
// completions advance nothing. Acquires the lock.
func (g *jitterGate) shutdown() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closed = true
	for _, h := range g.pending {
		if h.timer != nil {
			h.timer.Stop()
		}
	}
	g.pending = nil
}
