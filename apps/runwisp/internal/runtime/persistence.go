// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"sync"

	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
)

// RunPersistenceHook persists run state transitions. ctx is cancelled at
// Shutdown so a write already in flight at that moment can abort cleanly
// instead of hanging the daemon on a wedged DB. A task that hasn't started
// applying yet never sees an already-cancelled ctx — see worker.
type RunPersistenceHook func(ctx context.Context, run *model.Run, isNew bool)

// persistTask is the unit of work dispatched through the buffered channel.
// A typed struct avoids the per-call closure allocation that a chan func()
// would impose.
type persistTask struct {
	run   *model.Run
	isNew bool
	// done, when non-nil, marks a Flush sentinel: the worker closes it instead
	// of persisting. Because the channel is FIFO and a single worker drains it,
	// reaching the sentinel proves every earlier task has been applied.
	done chan struct{}
}

// PersistenceCoordinator manages async persistence of run state via a buffered
// channel. A single worker goroutine drains the channel so callers never block
// on I/O.
type PersistenceCoordinator struct {
	hook   RunPersistenceHook
	ch     chan persistTask
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewPersistenceCoordinator(bufferSize int) *PersistenceCoordinator {
	ctx, cancel := context.WithCancel(context.Background())
	pc := &PersistenceCoordinator{
		ch:     make(chan persistTask, bufferSize),
		ctx:    ctx,
		cancel: cancel,
	}
	pc.wg.Add(1)
	go pc.worker(ctx)
	return pc
}

// BindHook wires the persistence hook. If the executor supports
// SetRunUpdateCallback (optional interface), it is wired to persist
// run updates triggered by the executor (e.g. status changes).
func (pc *PersistenceCoordinator) BindHook(hook RunPersistenceHook, exec executor.Executor) {
	pc.hook = hook
	type runUpdateSetter interface {
		SetRunUpdateCallback(func(*model.Run))
	}
	if setter, ok := exec.(runUpdateSetter); ok {
		setter.SetRunUpdateCallback(func(run *model.Run) {
			pc.PersistExisting(run)
		})
	}
}

// PersistNew enqueues a create-style persistence task.
func (pc *PersistenceCoordinator) PersistNew(run *model.Run) {
	if pc.hook != nil {
		pc.enqueue(persistTask{run: run.Copy(), isNew: true})
	}
}

// PersistExisting enqueues an update-style persistence task.
func (pc *PersistenceCoordinator) PersistExisting(run *model.Run) {
	if pc.hook != nil {
		pc.enqueue(persistTask{run: run.Copy(), isNew: false})
	}
}

// Flush blocks until every task enqueued before this call has been applied by
// the worker. It enqueues a sentinel and waits for the worker to acknowledge
// it, giving tests a deterministic "persistence has caught up" barrier instead
// of a sleep. Returns immediately if the coordinator is shutting down.
func (pc *PersistenceCoordinator) Flush() {
	done := make(chan struct{})
	select {
	case <-pc.ctx.Done():
		return
	case pc.ch <- persistTask{done: done}:
	}
	select {
	case <-pc.ctx.Done():
	case <-done:
	}
}

// Shutdown stops the worker and waits for pending tasks to drain.
func (pc *PersistenceCoordinator) Shutdown() {
	pc.cancel()
	pc.wg.Wait()
}

func (pc *PersistenceCoordinator) enqueue(task persistTask) {
	select {
	case <-pc.ctx.Done():
		return
	case pc.ch <- task:
	}
}

func (pc *PersistenceCoordinator) worker(ctx context.Context) {
	defer pc.wg.Done()
	// drainCtx is what a task applies under once ctx is already cancelled —
	// whether because we're in the drain loop below, or because the select
	// case that read a queued task happened to fire in the same instant
	// ctx.Done() became ready (select picks between ready cases at random, so
	// that queued task must not be at the mercy of the coin flip: it hasn't
	// started applying yet, so cancelling it here would only drop data, never
	// bound a hang).
	drainCtx := context.WithoutCancel(ctx)
	for {
		select {
		case task := <-pc.ch:
			applyCtx := ctx
			if ctx.Err() != nil {
				applyCtx = drainCtx
			}
			pc.apply(applyCtx, task)
		case <-ctx.Done():
			for {
				select {
				case task := <-pc.ch:
					pc.apply(drainCtx, task)
				default:
					return
				}
			}
		}
	}
}

// apply runs one task: a Flush sentinel just gets acknowledged; everything
// else is persisted via the hook.
func (pc *PersistenceCoordinator) apply(ctx context.Context, task persistTask) {
	if task.done != nil {
		close(task.done)
		return
	}
	pc.hook(ctx, task.run, task.isNew)
}
