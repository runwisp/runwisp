// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"context"
	"sync"

	"github.com/runwisp/runwisp/internal/executor"
	"github.com/runwisp/runwisp/internal/model"
)

// RunPersistenceHook persists run state transitions.
type RunPersistenceHook func(run *model.Run, isNew bool)

// PersistenceCoordinator manages async persistence of run state via a buffered
// channel. A single worker goroutine drains the channel so callers never block
// on I/O.
type PersistenceCoordinator struct {
	hook   RunPersistenceHook
	ch     chan func()
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewPersistenceCoordinator(bufferSize int) *PersistenceCoordinator {
	ctx, cancel := context.WithCancel(context.Background())
	pc := &PersistenceCoordinator{
		ch:     make(chan func(), bufferSize),
		ctx:    ctx,
		cancel: cancel,
	}
	pc.wg.Add(1)
	go pc.worker()
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
		copiedRun := run.Copy()
		pc.enqueue(func() {
			pc.hook(copiedRun, true)
		})
	}
}

// PersistExisting enqueues an update-style persistence task.
func (pc *PersistenceCoordinator) PersistExisting(run *model.Run) {
	if pc.hook != nil {
		copiedRun := run.Copy()
		pc.enqueue(func() {
			pc.hook(copiedRun, false)
		})
	}
}

// Shutdown stops the worker and waits for pending tasks to drain.
func (pc *PersistenceCoordinator) Shutdown() {
	pc.cancel()
	pc.wg.Wait()
}

// Done returns the context's Done channel, used to detect shutdown.
func (pc *PersistenceCoordinator) Done() <-chan struct{} {
	return pc.ctx.Done()
}

func (pc *PersistenceCoordinator) enqueue(task func()) {
	select {
	case <-pc.ctx.Done():
		return
	case pc.ch <- task:
	}
}

func (pc *PersistenceCoordinator) worker() {
	defer pc.wg.Done()
	for {
		select {
		case task := <-pc.ch:
			task()
		case <-pc.ctx.Done():
			for {
				select {
				case task := <-pc.ch:
					task()
				default:
					return
				}
			}
		}
	}
}
