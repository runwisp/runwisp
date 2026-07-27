// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"sync"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/events"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/testutil"
)

// newTestManager builds a TaskManager backed by a MockExecutor and a fresh
// event bus, registering Shutdown as a cleanup. It collapses the 3-line setup
// repeated across the manager/services suites into one call. The concrete
// *defaultTaskManager is returned so tests can reach internal state when no
// public accessor exists.
func newTestManager(t *testing.T) (*defaultTaskManager, *testutil.MockExecutor, events.EventBus) {
	t.Helper()
	exec := new(testutil.MockExecutor)
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)
	t.Cleanup(jm.Shutdown)
	return jm.(*defaultTaskManager), exec, eb
}

// newGatedManager is like newTestManager but wires a GateExecutor, for tests
// that must hold a run "in flight" to exercise concurrency policy without
// timing guesses.
func newGatedManager(t *testing.T) (*defaultTaskManager, *testutil.GateExecutor, events.EventBus) {
	t.Helper()
	exec := testutil.NewGateExecutor()
	eb := events.NewEventBus()
	jm := NewTaskManager(exec, eb, time.Now)
	t.Cleanup(jm.Shutdown)
	return jm.(*defaultTaskManager), exec, eb
}

// runWaiter records run lifecycle events so a test can block until a target
// number have arrived — a deterministic replacement for sleeping and hoping
// the async run finished. Subscribe (via watchRuns/watchCompletions) before
// triggering the runs you intend to observe.
type runWaiter struct {
	mu   sync.Mutex
	runs []*model.Run
	bump chan struct{}
}

func watchRuns(eb events.EventBus, types ...events.EventType) *runWaiter {
	w := &runWaiter{bump: make(chan struct{}, 1)}
	handler := func(e events.Event) {
		re, ok := e.Data.(events.RunEvent)
		if !ok {
			return
		}
		w.mu.Lock()
		w.runs = append(w.runs, re.Run)
		w.mu.Unlock()
		select {
		case w.bump <- struct{}{}:
		default:
		}
	}
	for _, et := range types {
		eb.Subscribe(et, handler)
	}
	return w
}

// watchCompletions records terminal run events (completed + failed), the usual
// "the run finished" signal.
func watchCompletions(eb events.EventBus) *runWaiter {
	return watchRuns(eb, events.EventRunCompleted, events.EventRunFailed)
}

func (w *runWaiter) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.runs)
}

// waitFor blocks until at least n events have been recorded, failing the test
// on timeout so a wiring bug surfaces as a clear failure rather than a hang.
func (w *runWaiter) waitFor(t *testing.T, n int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for w.count() < n {
		select {
		case <-w.bump:
		case <-deadline:
			t.Fatalf("timed out waiting for %d run events, got %d", n, w.count())
		}
	}
}

// snapshot returns a copy of the recorded runs in arrival order.
func (w *runWaiter) snapshot() []*model.Run {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]*model.Run, len(w.runs))
	copy(out, w.runs)
	return out
}
