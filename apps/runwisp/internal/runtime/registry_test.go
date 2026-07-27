// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"sync"
	"testing"

	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskRegistry_GetSetDelete(t *testing.T) {
	reg := NewTaskRegistry(nil)

	_, ok := reg.Get("a")
	assert.False(t, ok, "empty registry must not find a task")

	a := &model.Task{Name: "a"}
	reg.Set(a)
	got, ok := reg.Get("a")
	require.True(t, ok)
	assert.Same(t, a, got, "Get returns the stored pointer, not a copy")
	assert.Equal(t, 1, reg.Len())

	// Set with the same name replaces the pointer (reload always swaps).
	a2 := &model.Task{Name: "a"}
	reg.Set(a2)
	got, _ = reg.Get("a")
	assert.Same(t, a2, got)
	assert.Equal(t, 1, reg.Len())

	reg.Delete("a")
	_, ok = reg.Get("a")
	assert.False(t, ok)
	assert.Equal(t, 0, reg.Len())

	// Delete of an absent name is a no-op.
	reg.Delete("missing")
}

func TestTaskRegistry_RangeAndSnapshot(t *testing.T) {
	reg := NewTaskRegistry(map[string]*model.Task{
		"a": {Name: "a"},
		"b": {Name: "b"},
	})

	seen := map[string]bool{}
	reg.Range(func(name string, task *model.Task) bool {
		seen[name] = true
		return true
	})
	assert.Equal(t, map[string]bool{"a": true, "b": true}, seen)

	// Returning false stops iteration after the first element.
	count := 0
	reg.Range(func(string, *model.Task) bool {
		count++
		return false
	})
	assert.Equal(t, 1, count)

	// Snapshot is a copy: mutating it must not affect the registry.
	snap := reg.Snapshot()
	snap["c"] = &model.Task{Name: "c"}
	assert.Equal(t, 2, reg.Len(), "mutating a snapshot must not touch the registry")
}

// TestTaskRegistry_ConcurrentReadWrite is the race-detector workhorse: it runs
// readers (Get/Range/Snapshot) concurrently with writers (Set/Delete). It must
// pass under `go test -race`.
func TestTaskRegistry_ConcurrentReadWrite(t *testing.T) {
	reg := NewTaskRegistry(map[string]*model.Task{"seed": {Name: "seed"}})

	var wg sync.WaitGroup
	const n = 50

	for i := range n {
		wg.Add(3)
		go func() {
			defer wg.Done()
			reg.Set(&model.Task{Name: "seed"})
		}()
		go func() {
			defer wg.Done()
			reg.Get("seed")
		}()
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				reg.Range(func(string, *model.Task) bool { return true })
			} else {
				reg.Snapshot()
			}
		}(i)
	}
	wg.Wait()
}
