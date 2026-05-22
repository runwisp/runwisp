// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"context"
	"time"

	"log/slog"

	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/storage"
)

// SoftDeleteTTL is the server-side window between a run being soft-deleted
// and the purger reclaiming it. The UI exposes a 5 s undo affordance; the
// 2 s buffer here absorbs edge-of-window clicks.
const SoftDeleteTTL = 7 * time.Second

// softDeletePurgeInterval is the cadence at which the purger sweeps for
// expired soft-deletes. Tight enough that the on-disk log file disappears
// promptly after the undo window; loose enough that the daemon doesn't
// spin reading nothing.
const softDeletePurgeInterval = 2 * time.Second

// SoftDeletePurger reclaims rows whose deleted_at is older than SoftDeleteTTL
// and removes their log files from disk. The undo window is a UI affordance,
// not a persistence contract — on boot, every still-soft-deleted row is
// drained before the periodic loop starts.
type SoftDeletePurger struct {
	db     storage.RunRepository
	logDir string
	cancel context.CancelFunc
}

// NewSoftDeletePurger wires the purger but does not start it.
func NewSoftDeletePurger(db storage.RunRepository, logDir string) *SoftDeletePurger {
	return &SoftDeletePurger{db: db, logDir: logDir}
}

// Start drains any leftover soft-deletes from a previous run and then begins
// the periodic sweep.
func (p *SoftDeletePurger) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.purge(ctx, 0) // boot drain — undo window is a UI affordance, not durability.
	go p.run(ctx)
}

// Stop halts the periodic sweep. Safe to call before Start.
func (p *SoftDeletePurger) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
}

func (p *SoftDeletePurger) run(ctx context.Context) {
	ticker := time.NewTicker(softDeletePurgeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.purge(ctx, SoftDeleteTTL)
		case <-ctx.Done():
			slog.Debug("Stopping soft-delete purger")
			return
		}
	}
}

func (p *SoftDeletePurger) purge(ctx context.Context, ttl time.Duration) {
	refs, err := p.db.PurgeExpiredSoftDeletes(ctx, ttl)
	if err != nil {
		slog.Warn("Soft-delete purge failed", "err", err)
		return
	}
	for _, ref := range refs {
		logPath := logutil.ResolveRunLogPath(p.logDir, ref.TaskName, ref.ID, ref.CreatedAt)
		logutil.RemoveLogFiles(logPath)
		logutil.RemoveEmptyParents(logPath, p.logDir)
	}
	if len(refs) > 0 {
		slog.Debug("Purged soft-deleted runs", "count", len(refs))
	}
}
