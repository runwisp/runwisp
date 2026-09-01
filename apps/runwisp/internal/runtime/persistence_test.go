// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/testutil"
	"github.com/stretchr/testify/assert"
)

func TestPersistenceCoordinatorPersistNew(t *testing.T) {
	pc := NewPersistenceCoordinator(10)
	defer pc.Shutdown()

	var called bool
	var isNewArg bool
	pc.hook = func(_ context.Context, run *model.Run, isNew bool) {
		called = true
		isNewArg = isNew
	}

	pc.PersistNew(&model.Run{TaskName: "test"})
	// Flush establishes a happens-before edge with the worker: the hook has run
	// and its writes are visible here, without a sleep or a data race.
	pc.Flush()

	assert.True(t, called)
	assert.True(t, isNewArg)
}

func TestPersistenceCoordinatorPersistExisting(t *testing.T) {
	pc := NewPersistenceCoordinator(10)
	defer pc.Shutdown()

	var isNewArg bool
	pc.hook = func(_ context.Context, run *model.Run, isNew bool) {
		isNewArg = isNew
	}

	pc.PersistExisting(&model.Run{TaskName: "test"})
	pc.Flush()

	assert.False(t, isNewArg)
}

func TestPersistenceCoordinatorNoHookNoPanic(t *testing.T) {
	pc := NewPersistenceCoordinator(10)
	defer pc.Shutdown()

	assert.NotPanics(t, func() {
		pc.PersistNew(&model.Run{})
		pc.PersistExisting(&model.Run{})
	})
}

func TestPersistenceCoordinatorShutdownDrains(t *testing.T) {
	pc := NewPersistenceCoordinator(100)

	var count int32
	pc.hook = func(_ context.Context, run *model.Run, isNew bool) {
		atomic.AddInt32(&count, 1)
	}

	for i := 0; i < 10; i++ {
		pc.PersistNew(&model.Run{})
	}

	pc.Shutdown()
	assert.Equal(t, int32(10), atomic.LoadInt32(&count))
}

func TestPersistenceCoordinatorAfterShutdown(t *testing.T) {
	pc := NewPersistenceCoordinator(10)
	pc.hook = func(_ context.Context, run *model.Run, isNew bool) {}
	pc.Shutdown()

	assert.NotPanics(t, func() {
		pc.PersistNew(&model.Run{})
		pc.PersistExisting(&model.Run{})
	})
}

// TestPersistenceCoordinatorShutdownDoesNotCancelAlreadyQueuedTask guards
// against a clean shutdown silently dropping a completed run's final write.
// The worker's select race is between "apply the queued task" and "ctx just
// got cancelled" — when both fire at once (a task already sitting in the
// buffered channel at the exact moment Shutdown cancels), Go's select picks
// between them pseudo-randomly. A task that hasn't started applying yet must
// never observe an already-cancelled context: there is nothing in-flight for
// the cancellation to bound, so applying it with a cancelled ctx only drops
// data. Runs the race window many times since a single trial can pass by luck.
func TestPersistenceCoordinatorShutdownDoesNotCancelAlreadyQueuedTask(t *testing.T) {
	for i := 0; i < 200; i++ {
		var gotCancelled atomic.Bool
		first := make(chan struct{})
		release := make(chan struct{})

		pc := NewPersistenceCoordinator(4)
		pc.hook = func(ctx context.Context, run *model.Run, isNew bool) {
			if run.TaskName == "first" {
				close(first)
				<-release // hold the worker here so "second" queues up behind it
				return
			}
			if ctx.Err() != nil {
				gotCancelled.Store(true)
			}
		}

		pc.PersistExisting(&model.Run{TaskName: "first"})
		<-first // worker is now blocked applying "first" with a still-live ctx

		pc.PersistExisting(&model.Run{TaskName: "second"}) // queues behind, unapplied

		pc.cancel() // ctx.Done() and pc.ch (holding "second") are now both ready

		close(release) // let "first" return; the worker's select re-evaluates
		pc.wg.Wait()   // drain "second" and exit

		if gotCancelled.Load() {
			t.Fatalf("iteration %d: a task queued before Shutdown was applied with an already-cancelled context", i)
		}
	}
}

func TestPublishRunNilBus(t *testing.T) {
	jm := NewTaskManager(new(testutil.MockExecutor), nil, time.Now).(*defaultTaskManager)
	assert.NotPanics(t, func() {
		jm.publishRun("test", &model.Run{})
	})
}
