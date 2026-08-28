// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// manualTimers is a deterministic afterFunc for gate tests. Instead of arming a
// wall-clock timer it records each breach callback, so a test fires them on
// demand (fireAll) and asserts how many are still armed (pending). It mirrors
// the injectable-timer seam used by internal/notify/coalesce, kept in-package
// because the runtime test binary cannot import a testutil that imports runtime.
type manualTimers struct {
	mu     sync.Mutex
	timers []*manualTimer
}

// manualTimer is one armed breach. Stop and fireAll both flip stopped under the
// parent's lock, so the gate's Stop() (called under gateMu) and the test's
// fireAll never race on it.
type manualTimer struct {
	owner   *manualTimers
	fn      func()
	stopped bool
}

func (t *manualTimer) Stop() bool {
	t.owner.mu.Lock()
	defer t.owner.mu.Unlock()
	was := t.stopped
	t.stopped = true
	return !was
}

func (m *manualTimers) after(_ time.Duration, fn func()) stopper {
	m.mu.Lock()
	defer m.mu.Unlock()
	t := &manualTimer{owner: m, fn: fn}
	m.timers = append(m.timers, t)
	return t
}

// pending reports how many armed breaches have neither fired nor been stopped
// (by a pull-forward or shutdown).
func (m *manualTimers) pending() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, t := range m.timers {
		if !t.stopped {
			n++
		}
	}
	return n
}

// fireAll invokes every still-armed breach exactly once, simulating each timer
// reaching its slot deadline. Callbacks run without the lock held so a breach
// can re-enter the gate (and Stop other timers) without deadlocking.
func (m *manualTimers) fireAll() {
	m.mu.Lock()
	var due []*manualTimer
	for _, t := range m.timers {
		if !t.stopped {
			t.stopped = true
			due = append(due, t)
		}
	}
	m.mu.Unlock()
	for _, t := range due {
		t.fn()
	}
}

// stepExecutor is an executor.Executor that lets a test release runs one at a
// time by ID, so the gate's pull-forward cascade can be driven deterministically
// (testutil.GateExecutor only releases every run at once). Each Execute
// announces its run ID, then blocks until that ID is released or its context is
// cancelled.
type stepExecutor struct {
	mu       sync.Mutex
	started  chan string
	releases map[string]chan struct{}
}

func newStepExecutor() *stepExecutor {
	return &stepExecutor{
		started:  make(chan string, 64),
		releases: make(map[string]chan struct{}),
	}
}

// gate returns the lazily created release channel for a run ID, so release and
// Execute rendezvous regardless of which one arrives first.
func (e *stepExecutor) gate(runID string) chan struct{} {
	e.mu.Lock()
	defer e.mu.Unlock()
	ch, ok := e.releases[runID]
	if !ok {
		ch = make(chan struct{})
		e.releases[runID] = ch
	}
	return ch
}

func (e *stepExecutor) Execute(ctx context.Context, _ *model.Task, run *model.Run) *executor.ExecuteResult {
	ch := e.gate(run.ID)
	// The started-send honours cancellation so a never-released run still drains
	// on shutdown instead of wedging the manager's wg.Wait.
	select {
	case e.started <- run.ID:
	case <-ctx.Done():
		return &executor.ExecuteResult{ExitCode: -1, Stopped: true, Error: ctx.Err()}
	}
	select {
	case <-ch:
		return &executor.ExecuteResult{ExitCode: 0}
	case <-ctx.Done():
		return &executor.ExecuteResult{ExitCode: -1, Stopped: true, Error: ctx.Err()}
	}
}

func (e *stepExecutor) Availability() executor.Availability { return executor.Availability{} }

// release unblocks the run with the given ID, creating the rendezvous channel
// first if Execute has not reached it yet.
func (e *stepExecutor) release(runID string) { close(e.gate(runID)) }

// waitStarted blocks until some run begins executing and returns its ID,
// failing on timeout so a wiring bug surfaces as a failure, not a hang.
func (e *stepExecutor) waitStarted(t *testing.T) string {
	t.Helper()
	select {
	case id := <-e.started:
		return id
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a run to start executing")
		return ""
	}
}

// noMoreStarts asserts no further run reached the executor, the way a test
// confirms an abandoned fire never ran.
func (e *stepExecutor) noMoreStarts(t *testing.T) {
	t.Helper()
	select {
	case id := <-e.started:
		t.Fatalf("an unexpected run started executing: %s", id)
	default:
	}
}

// newJitterTestManager wires a real defaultTaskManager onto a step executor and
// an injected clock, then swaps the gate's timer seam for a manualTimers so
// breaches fire on demand. The concrete manager is returned so a test can call
// ScheduleJitteredRun directly and reach gate state.
func newJitterTestManager(t *testing.T, clock func() time.Time) (*defaultTaskManager, *stepExecutor, *manualTimers, *events.Bus) {
	t.Helper()
	exec := newStepExecutor()
	eb := events.NewEventBus()
	jm, ok := NewTaskManager(exec, eb, clock).(*defaultTaskManager)
	require.True(t, ok)
	mt := &manualTimers{}
	// Swap before any submit: the gate is untouched until the first
	// ScheduleJitteredRun, so this is a plain assignment, not a racing one.
	jm.gate.after = mt.after
	t.Cleanup(jm.Shutdown)
	return jm, exec, mt, eb
}

// TestJitterGate_PullsForwardWhenIdle proves the gate is work-conserving: with
// three short jittered fires queued, each runs the instant the prior finishes —
// back-to-back, never smeared out to its staggered slot — and no breach timer
// ever fires.
func TestJitterGate_PullsForwardWhenIdle(t *testing.T) {
	clk := testutil.NewClock(time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC))
	jm, exec, mt, eb := newJitterTestManager(t, clk.Now)
	for _, n := range []string{"a", "b", "c"} {
		jm.UpsertTask(testTask(n, model.PolicySkip, 1))
	}
	done := watchCompletions(eb)

	tick := clk.Now()
	// a has the earliest slot (offset 0); b and c are staggered 5m and 10m on.
	jm.ScheduleJitteredRun("a", tick, tick, 10*time.Minute)
	idA := exec.waitStarted(t) // the gate is free, so a fires at once and blocks

	jm.ScheduleJitteredRun("b", tick, tick.Add(5*time.Minute), 10*time.Minute)
	jm.ScheduleJitteredRun("c", tick, tick.Add(10*time.Minute), 10*time.Minute)

	// b and c wait on the gate, not the wall clock: their breach timers are
	// armed but neither has started a run.
	assert.Equal(t, 2, mt.pending(), "b and c are held behind a")
	assert.Equal(t, 0, jm.GetActiveRunCount("b"))
	assert.Equal(t, 0, jm.GetActiveRunCount("c"))

	// Draining each run pulls the next forward immediately — no clock advance,
	// no breach.
	exec.release(idA)
	exec.release(exec.waitStarted(t)) // b, pulled forward
	exec.release(exec.waitStarted(t)) // c, pulled forward
	done.waitFor(t, 3)

	assert.Equal(t, 0, mt.pending(), "every held fire was pulled forward; none breached")
}

// TestJitterGate_ReleasesEarliestSlotFirst proves the gate drains in
// earliest-deadline-first order, not submission order: a later-slot fire
// submitted first still releases after an earlier-slot fire submitted second.
func TestJitterGate_ReleasesEarliestSlotFirst(t *testing.T) {
	clk := testutil.NewClock(time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC))
	jm, exec, _, eb := newJitterTestManager(t, clk.Now)
	for _, n := range []string{"blk", "late", "early"} {
		jm.UpsertTask(testTask(n, model.PolicySkip, 1))
	}
	starts := watchRuns(eb, events.EventRunStarted)

	tick := clk.Now()
	jm.ScheduleJitteredRun("blk", tick, tick, 30*time.Minute) // holds the gate
	idBlk := exec.waitStarted(t)

	// Submit out of slot order: late (slot +20m) before early (slot +10m).
	jm.ScheduleJitteredRun("late", tick, tick.Add(20*time.Minute), 30*time.Minute)
	jm.ScheduleJitteredRun("early", tick, tick.Add(10*time.Minute), 30*time.Minute)

	// Drain the gate; each completion pulls the next earliest slot forward.
	exec.release(idBlk)
	exec.release(exec.waitStarted(t))
	exec.release(exec.waitStarted(t))
	starts.waitFor(t, 3)

	order := make([]string, 0, 3)
	for _, r := range starts.snapshot() {
		order = append(order, r.TaskName)
	}
	assert.Equal(t, []string{"blk", "early", "late"}, order,
		"held fires release earliest-slot first (EDF), regardless of submit order")
}

// TestJitterGate_BreachesHeldFiresAtSlot proves the slot is a real deadline:
// when the gate stays congested (the holder never finishes), each held fire
// runs anyway when its slot timer fires — staggered across the window rather
// than bursting, and concurrent with the still-running holder.
func TestJitterGate_BreachesHeldFiresAtSlot(t *testing.T) {
	clk := testutil.NewClock(time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC))
	jm, exec, mt, _ := newJitterTestManager(t, clk.Now)
	for _, n := range []string{"a", "b", "c"} {
		jm.UpsertTask(testTask(n, model.PolicySkip, 1))
	}

	tick := clk.Now()
	jm.ScheduleJitteredRun("a", tick, tick, 30*time.Minute)
	_ = exec.waitStarted(t) // a holds the gate and is never released

	jm.ScheduleJitteredRun("b", tick, tick.Add(10*time.Minute), 30*time.Minute)
	jm.ScheduleJitteredRun("c", tick, tick.Add(20*time.Minute), 30*time.Minute)
	require.Equal(t, 2, mt.pending(), "b and c are held behind the long-running a")

	// Each slot deadline arrives while a is still in flight: b and c breach and
	// run concurrently with it.
	mt.fireAll()
	_ = exec.waitStarted(t)
	_ = exec.waitStarted(t)

	assert.Equal(t, 1, jm.GetActiveRunCount("a"), "the holder is still in flight")
	assert.Equal(t, 1, jm.GetActiveRunCount("b"), "b breached at its slot and runs")
	assert.Equal(t, 1, jm.GetActiveRunCount("c"), "c breached at its slot and runs")
	assert.Equal(t, 0, mt.pending(), "both slot timers have fired")
}

// TestJitterGate_UntrackedCompletionDoesNotAdvance proves only a gate-triggered
// run frees the gate. A plain (un-jittered) run completing must not pull a held
// jittered fire forward. Services share this path — the gate only tracks runs
// it triggered, and it never triggers a service or a plain task.
func TestJitterGate_UntrackedCompletionDoesNotAdvance(t *testing.T) {
	clk := testutil.NewClock(time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC))
	jm, exec, mt, eb := newJitterTestManager(t, clk.Now)
	for _, n := range []string{"jit-a", "jit-b", "plain"} {
		jm.UpsertTask(testTask(n, model.PolicySkip, 1))
	}
	done := watchCompletions(eb)

	tick := clk.Now()
	jm.ScheduleJitteredRun("jit-a", tick, tick, 30*time.Minute) // holds the gate
	idA := exec.waitStarted(t)
	jm.ScheduleJitteredRun("jit-b", tick, tick.Add(10*time.Minute), 30*time.Minute)
	require.Equal(t, 1, mt.pending(), "jit-b is held behind jit-a")

	// A plain run starts and finishes. Its completion is not a gate completion.
	_, err := jm.TriggerRun("plain", model.TriggeredByAPI)
	require.NoError(t, err)
	exec.release(exec.waitStarted(t))
	done.waitFor(t, 1) // only the plain run has completed

	assert.Equal(t, 1, mt.pending(), "jit-b is still held — a plain run never frees the gate")
	assert.Equal(t, 0, jm.GetActiveRunCount("jit-b"))

	// Only the gate-held jit-a finishing advances jit-b.
	exec.release(idA)
	_ = exec.waitStarted(t)
	assert.Equal(t, 1, jm.GetActiveRunCount("jit-b"), "jit-a completing pulls jit-b forward")
}

// TestJitterGate_OverrunStopsBlockingPastWindow proves an in-flight fire only
// blocks peers for the length of its own window. Once a holder overruns its
// window, a fresh submission runs immediately even though the holder is still
// executing — the gate targets one run at a time but won't stall forever on a
// runaway.
func TestJitterGate_OverrunStopsBlockingPastWindow(t *testing.T) {
	clk := testutil.NewClock(time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC))
	jm, exec, _, _ := newJitterTestManager(t, clk.Now)
	jm.UpsertTask(testTask("long", model.PolicySkip, 1))
	jm.UpsertTask(testTask("waiter", model.PolicySkip, 1))

	tick := clk.Now()
	jm.ScheduleJitteredRun("long", tick, tick, 10*time.Minute) // 10m window
	_ = exec.waitStarted(t)                                    // never released — it overruns

	// Time passes beyond long's window while it is still running.
	clk.Advance(11 * time.Minute)

	now := clk.Now()
	jm.ScheduleJitteredRun("waiter", now, now, 10*time.Minute)
	_ = exec.waitStarted(t) // free: long has aged past waiter's horizon

	assert.Equal(t, 1, jm.GetActiveRunCount("long"), "the overrunning holder is still in flight")
	assert.Equal(t, 1, jm.GetActiveRunCount("waiter"), "but it no longer blocks a fresh fire past its window")
}

// TestJitterGate_ShutdownAbandonsPending proves shutdown drops every held fire
// and stops its timer, so nothing starts a run after the daemon begins shutting
// down — even if a stray slot timer fires afterwards.
func TestJitterGate_ShutdownAbandonsPending(t *testing.T) {
	clk := testutil.NewClock(time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC))
	jm, exec, mt, _ := newJitterTestManager(t, clk.Now)
	jm.UpsertTask(testTask("a", model.PolicySkip, 1))
	jm.UpsertTask(testTask("b", model.PolicySkip, 1))

	tick := clk.Now()
	jm.ScheduleJitteredRun("a", tick, tick, 30*time.Minute) // holds the gate
	_ = exec.waitStarted(t)
	jm.ScheduleJitteredRun("b", tick, tick.Add(10*time.Minute), 30*time.Minute)
	require.Equal(t, 1, mt.pending(), "b is held")

	jm.Shutdown() // cancels a, abandons b

	assert.Equal(t, 0, mt.pending(), "shutdown stopped the held fire's breach timer")
	mt.fireAll() // a stray fire must still not start b
	assert.Equal(t, 0, jm.GetActiveRunCount("b"), "no run is created for the abandoned fire")
	exec.noMoreStarts(t)
}

// gateInflightIDs snapshots the gate's in-flight run IDs under its lock.
func gateInflightIDs(g *jitterGate) map[string]bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make(map[string]bool, len(g.inflight))
	for id := range g.inflight {
		out[id] = true
	}
	return out
}

// TestJitterGate_RemoveTaskRetiresOrphanedQueuedRunFromGate is the bug: a
// jittered fire that breaches into the manager's own per-task queue (because
// OnOverlap=Queue and the concurrency slot is already taken by an earlier
// gate-fired run) is recorded as in-flight in the gate — by design, since it
// still has to run eventually. But if the task is removed (a `runwisp
// reload`) while that run is still sitting in the queue, RemoveTask finalizes
// it via endOrphanedPending without ever telling the gate, so it never runs
// and never completes — leaving a permanent stale entry in the gate's
// in-flight set that (for one jitter window) makes freeFor() wrongly report
// the gate as busy for unrelated tasks, and leaks forever after that.
func TestJitterGate_RemoveTaskRetiresOrphanedQueuedRunFromGate(t *testing.T) {
	clk := testutil.NewClock(time.Date(2026, 6, 10, 3, 0, 0, 0, time.UTC))
	jm, exec, mt, _ := newJitterTestManager(t, clk.Now)
	jm.UpsertTask(testTask("t", model.PolicyQueue, 1))

	tick := clk.Now()
	jm.ScheduleJitteredRun("t", tick, tick, 30*time.Minute) // gate free: pulled forward at once
	idA := exec.waitStarted(t)                              // occupies t's only concurrency slot

	// A second tick for the same task: the daemon-wide gate is busy (a is
	// in flight), so this one is held — then forced through by its breach
	// timer despite the gate being congested, straight into t's own queue
	// (OnOverlap=Queue, MaxConcurrent=1, a still holds the slot).
	jm.ScheduleJitteredRun("t", tick.Add(time.Minute), tick.Add(time.Minute), 30*time.Minute)
	require.Equal(t, 1, mt.pending(), "b is held behind a")
	mt.fireAll()
	exec.noMoreStarts(t) // b is sitting in t's queue, not executing

	inflight := gateInflightIDs(jm.gate)
	require.Len(t, inflight, 2, "both the running a and the queued b are tracked in-flight")
	require.True(t, inflight[idA])

	// A reload removes the task while b is still queued.
	jm.RemoveTask("t")

	after := gateInflightIDs(jm.gate)
	assert.Len(t, after, 1, "the orphaned queued run must be retired from the gate's in-flight set")
	assert.True(t, after[idA], "only the still-running a may remain in-flight")
}
