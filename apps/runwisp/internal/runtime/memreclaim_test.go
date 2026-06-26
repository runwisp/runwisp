// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoryReclaimer_RunsReclaimAndShrinkOnTick(t *testing.T) {
	var reclaims, shrinks atomic.Int64
	r := NewMemoryReclaimer(func(context.Context) error {
		shrinks.Add(1)
		return nil
	})
	r.reclaim = func() { reclaims.Add(1) }
	r.interval = time.Millisecond

	r.Start()
	defer r.Stop()

	deadline := time.After(2 * time.Second)
	for reclaims.Load() == 0 || shrinks.Load() == 0 {
		select {
		case <-deadline:
			t.Fatalf("reclaimer did not fire: reclaims=%d shrinks=%d", reclaims.Load(), shrinks.Load())
		case <-time.After(time.Millisecond):
		}
	}
}

func TestMemoryReclaimer_StopEndsLoop(t *testing.T) {
	var reclaims atomic.Int64
	r := NewMemoryReclaimer(nil) // nil shrink hook must be tolerated
	r.reclaim = func() { reclaims.Add(1) }
	r.interval = time.Millisecond

	r.Start()
	// Let it tick a few times.
	for reclaims.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	r.Stop()

	settled := reclaims.Load()
	time.Sleep(20 * time.Millisecond)
	if grew := reclaims.Load() - settled; grew > 1 {
		t.Fatalf("reclaimer kept firing after Stop: %d extra ticks", grew)
	}
}

func TestResolveReclaimInterval(t *testing.T) {
	t.Run("default when unset", func(t *testing.T) {
		t.Setenv(MemReclaimIntervalEnv, "")
		// t.Setenv with "" still sets the key; treat empty as parse failure →
		// default. Verify the unset path via a valid override below instead.
		if got := resolveReclaimInterval(); got != DefaultMemReclaimInterval {
			t.Fatalf("want default %v, got %v", DefaultMemReclaimInterval, got)
		}
	})
	t.Run("valid override", func(t *testing.T) {
		t.Setenv(MemReclaimIntervalEnv, "30s")
		if got := resolveReclaimInterval(); got != 30*time.Second {
			t.Fatalf("want 30s, got %v", got)
		}
	})
	t.Run("invalid falls back to default", func(t *testing.T) {
		t.Setenv(MemReclaimIntervalEnv, "not-a-duration")
		if got := resolveReclaimInterval(); got != DefaultMemReclaimInterval {
			t.Fatalf("want default %v, got %v", DefaultMemReclaimInterval, got)
		}
	})
}
