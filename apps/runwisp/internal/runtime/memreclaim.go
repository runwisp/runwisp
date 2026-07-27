// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package runtime

import (
	"context"
	"os"
	rdebug "runtime/debug"
	"time"

	"log/slog"
)

// DefaultMemReclaimInterval is how often the reclaimer returns freed heap to
// the OS at steady state. Decoupled from retention so RSS is reclaimed even
// when retention is disabled or a sweep finds nothing to delete.
const DefaultMemReclaimInterval = 10 * time.Minute

// MemReclaimIntervalEnv lets operators tune the cadence via a Go duration
// string (e.g. "5m"). Env-only by design — it is an operational knob, not part
// of the TOML task surface.
const MemReclaimIntervalEnv = "RUNWISP_MEM_RECLAIM_INTERVAL"

// MemoryReclaimer periodically forces a GC + scavenge (debug.FreeOSMemory) and
// runs an optional shrink hook (e.g. SQLite PRAGMA shrink_memory), so the
// daemon's RSS tracks its live working set instead of sitting at the
// high-water mark left by transient spikes — log streaming, large log-page
// reads, retention batches. One full GC per interval is negligible at idle.
type MemoryReclaimer struct {
	interval time.Duration
	// reclaim returns freed heap to the OS. A field (not a direct
	// debug.FreeOSMemory call) so tests assert cadence without a real GC.
	reclaim func()
	// shrink releases caches held by downstream stores back to their allocator;
	// nil when the store has nothing to shrink.
	shrink func(context.Context) error
	cancel context.CancelFunc
}

// NewMemoryReclaimer builds a reclaimer at the default cadence (overridable via
// RUNWISP_MEM_RECLAIM_INTERVAL). shrink may be nil.
func NewMemoryReclaimer(shrink func(context.Context) error) *MemoryReclaimer {
	return &MemoryReclaimer{
		interval: resolveReclaimInterval(),
		reclaim:  rdebug.FreeOSMemory,
		shrink:   shrink,
	}
}

func resolveReclaimInterval() time.Duration {
	raw, ok := os.LookupEnv(MemReclaimIntervalEnv)
	if !ok {
		return DefaultMemReclaimInterval
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		slog.Warn("invalid "+MemReclaimIntervalEnv+"; using default",
			"value", raw, "default", DefaultMemReclaimInterval)
		return DefaultMemReclaimInterval
	}
	return d
}

func (r *MemoryReclaimer) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	go r.run(ctx)
}

func (r *MemoryReclaimer) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
}

func (r *MemoryReclaimer) run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.reclaimOnce(ctx)
		case <-ctx.Done():
			slog.Debug("Stopping memory reclaimer")
			return
		}
	}
}

// reclaimOnce shrinks downstream caches first (so the freed pages become
// collectable), then forces the Go runtime to hand free heap back to the OS.
func (r *MemoryReclaimer) reclaimOnce(ctx context.Context) {
	if r.shrink != nil {
		if err := r.shrink(ctx); err != nil {
			slog.Debug("memory reclaim shrink hook failed", "err", err)
		}
	}
	r.reclaim()
}
