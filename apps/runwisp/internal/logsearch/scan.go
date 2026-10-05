// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package logsearch

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/runwisp/runwisp/internal/logutil"
	"golang.org/x/sync/errgroup"
)

const (
	// DefaultMaxHits caps the result set when the caller did not specify one.
	DefaultMaxHits = 200

	// MaxHitsCeiling is the hard upper bound enforced by ScanTask. The HTTP
	// layer enforces its own (tighter) maximum via huma validation, but the
	// library applies its own clamp so direct callers cannot ask for an
	// unbounded result set.
	MaxHitsCeiling = 1000

	// ScanWorkers bounds parallel I/O across runs. Four is enough to keep a
	// single spinning disk saturated and avoids hammering shared SSDs.
	ScanWorkers = 4
)

// Hit is one match — exactly what the wire format returns. TS is the run's
// CreatedAt in Unix ms; the per-line timestamp would require parsing a
// timestamp prefix that not every line carries, and the run timestamp is
// already what sorts the result set newest-first.
type Hit struct {
	RunID  string `json:"run_id"`
	N      int64  `json:"n"`
	Stream string `json:"stream"`
	Text   string `json:"text"`
	TS     int64  `json:"ts"`
}

// RunRef identifies one run on disk for a task-wide scan. Ordered by the
// caller — ScanTask preserves that order in its output.
type RunRef struct {
	ID        string
	LogPath   string
	CreatedAt time.Time
}

// ScanOpts controls a scan.
type ScanOpts struct {
	// MaxHits caps the returned hit slice. Zero or negative falls back to
	// DefaultMaxHits; values above MaxHitsCeiling are clamped down.
	MaxHits int
}

// Cursor is an opaque continuation token. The endpoint encodes it as
// base64-JSON; ScanTask does not care about the encoding. NextN is the first
// line of RunID still to scan, so 0 unambiguously means "from the start".
type Cursor struct {
	RunID string `json:"run_id"`
	NextN int64  `json:"next_n"`
}

// ScanRun matches one run end-to-end. Stops at maxHits or when ctx is done.
// The returned slice is ordered by ascending line number — that's the order
// emitted by logutil.ScanLines and is what the UI displays per-run.
//
// Lines numbered below firstN are skipped, so firstN=0 scans the whole run.
// This is how a paginated cursor resumes mid-run without re-emitting hits
// the previous page already returned. more=true means the run still has
// unscanned bytes — the caller can resume from hits[last].N+1.
func ScanRun(ctx context.Context, run RunRef, m Matcher, maxHits int, firstN int64) (hits []Hit, more bool, err error) {
	// Clamp here (not only in ScanTask) so direct callers — the station
	// log-search path reaches ScanRun without going through ScanTask — cannot
	// ask for an unbounded in-memory result set.
	maxHits = clampMaxHits(maxHits)
	tsMs := run.CreatedAt.UnixMilli()
	hits = make([]Hit, 0, min(maxHits, 16))
	err = logutil.ScanLines(ctx, run.LogPath, func(rec logutil.LogLineRecord) bool {
		if rec.LineNum < firstN {
			return true
		}
		if !m.Match([]byte(rec.Text)) {
			return true
		}
		hits = append(hits, Hit{
			RunID:  run.ID,
			N:      rec.LineNum,
			Stream: rec.Stream,
			Text:   rec.Text,
			TS:     tsMs,
		})
		if len(hits) >= maxHits {
			more = true
			return false
		}
		return true
	})
	return hits, more, err
}

// ScanTask scans `runs` in newest-first order (caller-supplied), running up
// to ScanWorkers scans in parallel. The result slice is sorted to match the
// input order; within a run, hits keep their on-disk (ascending-line) order.
//
// When the result slice would exceed MaxHits, ScanTask stops scanning
// further runs, truncates the slice, and emits a Cursor pointing at the
// next run/line to resume from on the following request.
func ScanTask(ctx context.Context, runs []RunRef, matcherFactory func() Matcher, opts ScanOpts, startAfterRunID string, firstN int64) ([]Hit, *Cursor, int, error) {
	maxHits := clampMaxHits(opts.MaxHits)
	if len(runs) == 0 {
		return nil, nil, 0, nil
	}

	// Skip already-consumed runs from a previous page. The cursor's
	// `firstN` only applies to the run it names; subsequent runs in
	// the same page restart at line 0.
	start := runStartIndex(runs, startAfterRunID)
	if start < 0 {
		// The cursor's run left the window (retention deleted it). Its line
		// offset says nothing about runs[0], so scan that run from the start.
		start, firstN = 0, 0
	}
	pending := runs[start:]
	if len(pending) == 0 {
		return nil, nil, 0, nil
	}

	results, scanned, err := scanPending(ctx, pending, matcherFactory, maxHits, firstN)
	if err != nil {
		return nil, nil, scanned, err
	}

	flat, cursor := flattenResults(results, pending, maxHits)
	sortHits(flat)
	return flat, cursor, scanned, nil
}

// runResult holds one run's hits plus whether the run still has unscanned
// bytes (its per-run cap was hit).
type runResult struct {
	hits []Hit
	more bool
}

// clampMaxHits normalizes a caller-supplied cap: non-positive falls back to
// DefaultMaxHits, oversized clamps down to MaxHitsCeiling.
func clampMaxHits(maxHits int) int {
	if maxHits <= 0 {
		return DefaultMaxHits
	}
	return min(maxHits, MaxHitsCeiling)
}

// runStartIndex returns the index of the run named by startAfterRunID, 0 when
// the ID is empty, or -1 when it is absent from runs.
func runStartIndex(runs []RunRef, startAfterRunID string) int {
	if startAfterRunID == "" {
		return 0
	}
	for i, r := range runs {
		if r.ID == startAfterRunID {
			return i
		}
	}
	return -1
}

// scanPending scans every pending run in parallel (bounded by ScanWorkers),
// returning per-run results in input order plus the count of runs scanned.
// Only the first run honors firstN; later runs restart at line 0.
//
// A single hit budget (remaining) is shared across the scans: a run beyond
// the first ScanWorkers only starts once an earlier scan freed its slot, so
// it caps itself at what is still missing, or skips entirely once maxHits is
// satisfied. The initial wave each gets the full maxHits, since sharing a
// budget between concurrent starts would make results depend on goroutine
// scheduling.
func scanPending(ctx context.Context, pending []RunRef, matcherFactory func() Matcher, maxHits int, firstN int64) ([]runResult, int, error) {
	results := make([]runResult, len(pending))

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(ScanWorkers)

	var (
		mu        sync.Mutex
		scanned   int
		remaining atomic.Int64
	)
	remaining.Store(int64(maxHits))
	for i, r := range pending {
		var skipN int64
		if i == 0 {
			skipN = firstN
		}
		g.Go(func() error {
			perRunMax := maxHits
			if i >= ScanWorkers {
				budget := remaining.Load()
				if budget <= 0 {
					// Every worker slot this run waited on was freed by a
					// scan that, combined, already found maxHits hits.
					// Nothing this run finds would survive flattenResults,
					// so don't scan it at all — flattenResults still emits a
					// cursor pointing here since it was never visited.
					return nil
				}
				perRunMax = int(budget)
			}
			hits, more, err := ScanRun(gctx, r, matcherFactory(), perRunMax, skipN)
			if err != nil {
				return err
			}
			remaining.Add(-int64(len(hits)))
			mu.Lock()
			results[i] = runResult{hits: hits, more: more}
			scanned++
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, scanned, err
	}
	return results, scanned, nil
}

// flattenResults concatenates per-run hits in the caller's run order, stopping
// once maxHits is reached and emitting a Cursor pointing at the next run/line
// to resume from. A run that delivered its full per-run cap (more=true) leaves
// a cursor even when the global slice still has room.
func flattenResults(results []runResult, pending []RunRef, maxHits int) ([]Hit, *Cursor) {
	flat := make([]Hit, 0, maxHits)
	for i, r := range results {
		room := maxHits - len(flat)
		if room <= 0 {
			// A previous run filled the budget exactly at its boundary. This
			// run's hits were scanned but not emitted, so resume from its
			// first line next request rather than silently dropping it.
			return flat, &Cursor{RunID: pending[i].ID, NextN: 0}
		}
		if len(r.hits) <= room {
			flat = append(flat, r.hits...)
			if r.more {
				return flat, &Cursor{RunID: pending[i].ID, NextN: r.hits[len(r.hits)-1].N + 1}
			}
			continue
		}
		flat = append(flat, r.hits[:room]...)
		return flat, &Cursor{RunID: pending[i].ID, NextN: r.hits[room-1].N + 1}
	}
	return flat, nil
}

// sortHits enforces a deterministic newest-first ordering by run timestamp,
// then ascending line within a run. Workers complete out of order, so append
// order alone cannot be trusted.
func sortHits(flat []Hit) {
	slices.SortStableFunc(flat, func(a, b Hit) int {
		return cmp.Or(
			cmp.Compare(b.TS, a.TS),
			cmp.Compare(b.RunID, a.RunID),
			cmp.Compare(a.N, b.N),
		)
	})
}
