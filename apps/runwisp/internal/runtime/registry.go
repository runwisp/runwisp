// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"sync"

	"github.com/runwisp/runwisp/internal/model"
)

// TaskRegistry is the single guarded owner of the daemon's in-memory task set.
//
// The task map is built once at boot and then read lock-free by the scheduler
// (at start), the retention cleaner (background ticker), and the server's run
// service (every HTTP request). Once `runwisp reload` can mutate the set while
// the daemon runs, those reads become a data race — so every long-lived reader
// goes through this RWMutex-guarded accessor instead of touching the bare map.
//
// Reload never mutates a *model.Task in place: it Sets a fresh pointer. Runs
// already in flight keep the old pointer they captured, preserving the
// single-writer-per-task invariant.
type TaskRegistry struct {
	mu    sync.RWMutex
	tasks map[string]*model.Task
}

// NewTaskRegistry wraps an existing task map. The registry takes ownership of
// the map; callers must not retain or mutate it afterwards.
func NewTaskRegistry(tasks map[string]*model.Task) *TaskRegistry {
	if tasks == nil {
		tasks = make(map[string]*model.Task)
	}
	return &TaskRegistry{tasks: tasks}
}

// Get returns the task registered under name, if any.
func (r *TaskRegistry) Get(name string) (*model.Task, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	task, ok := r.tasks[name]
	return task, ok
}

// Range calls fn for each registered task while holding the read lock. fn must
// not call back into the registry (it would deadlock) and must not retain the
// pointer past a possible reload. Returning false stops iteration early.
func (r *TaskRegistry) Range(fn func(name string, task *model.Task) bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for name, task := range r.tasks {
		if !fn(name, task) {
			return
		}
	}
}

// Snapshot returns a shallow copy of the task map. Safe to range over without
// holding the lock; the *model.Task pointers are shared, not copied.
func (r *TaskRegistry) Snapshot() map[string]*model.Task {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]*model.Task, len(r.tasks))
	for name, task := range r.tasks {
		out[name] = task
	}
	return out
}

// Set inserts or replaces the task registered under task.Name.
func (r *TaskRegistry) Set(task *model.Task) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tasks[task.Name] = task
}

// Delete removes the task registered under name. No-op if absent.
func (r *TaskRegistry) Delete(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tasks, name)
}

// Len reports how many tasks are registered.
func (r *TaskRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tasks)
}
