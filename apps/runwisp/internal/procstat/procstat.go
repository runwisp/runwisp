// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package procstat samples the CPU and memory use of running shell runs, one
// process group per run.
//
// It exists because the kernel's own at-exit accounting can't answer "how much
// memory did this run use": Linux folds the parent's RSS high-water mark into a
// child at vfork+exec, so every run's ru_maxrss is floored at the daemon's own
// peak. Per-process VmHWM is not, so the peak comes from sampling while the
// run is alive.
package procstat

import (
	"sync"
	"time"

	"github.com/runwisp/runwisp/internal/crashguard"
	"github.com/runwisp/runwisp/internal/model"
)

// groupStat is one reading of a process group, summed over its live members.
type groupStat struct {
	CPU  time.Duration // cumulative user+system time
	RSS  int64         // current resident bytes
	Peak int64         // per-process high-water marks (VmHWM), or RSS where unknown
}

// reader reads the requested process groups; groups with no live member are
// absent from the result.
type reader func(pgids map[int]struct{}) map[int]groupStat

const (
	// youngFor is how long a run is sampled at fastInterval, so short cron
	// runs still get a memory reading before they exit.
	youngFor     = 2 * time.Second
	slowInterval = time.Second
)

// Sampler samples every tracked run from one goroutine, which runs only while
// at least one run is tracked. All methods are safe for concurrent use.
type Sampler struct {
	read reader
	now  func() time.Time
	fast time.Duration

	mu      sync.Mutex
	runs    map[string]*tracked // by run ID
	running bool
}

type tracked struct {
	task    string
	pgid    int
	started time.Time

	sampled bool
	prevCPU time.Duration
	prevAt  time.Time
	cpuPct  float64
	rss     int64
	peak    int64
}

// New returns a sampler for the current platform. On platforms it cannot
// measure, every run reads as unsampled.
func New() *Sampler {
	return newSampler(platformReader(), time.Now, fastInterval)
}

func newSampler(read reader, now func() time.Time, fast time.Duration) *Sampler {
	return &Sampler{read: read, now: now, fast: fast, runs: make(map[string]*tracked)}
}

// Track starts sampling the process group pgid for a run. Call stop after the
// process has been reaped; it returns the highest memory reading seen, and
// ok=false when the run was never sampled (it exited within the first tick).
func (s *Sampler) Track(taskName, runID string, pgid int) (stop func() (peakBytes int64, ok bool)) {
	s.mu.Lock()
	s.runs[runID] = &tracked{task: taskName, pgid: pgid, started: s.now()}
	if !s.running {
		s.running = true
		go s.loop()
	}
	s.mu.Unlock()

	return func() (int64, bool) {
		s.mu.Lock()
		defer s.mu.Unlock()
		t := s.runs[runID]
		delete(s.runs, runID)
		if t == nil || !t.sampled {
			return 0, false
		}
		return t.peak, true
	}
}

// TaskUsage sums the latest reading of every sampled run per task.
func (s *Sampler) TaskUsage() map[string]model.ResourceUsage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]model.ResourceUsage)
	for _, t := range s.runs {
		if !t.sampled {
			continue
		}
		u := out[t.task]
		u.CPUPercent += t.cpuPct
		u.MemoryBytes += t.rss
		out[t.task] = u
	}
	return out
}

// RunUsage is the latest reading of every sampled run, keyed by run ID.
func (s *Sampler) RunUsage() map[string]model.ResourceUsage {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]model.ResourceUsage)
	for id, t := range s.runs {
		if t.sampled {
			out[id] = model.ResourceUsage{CPUPercent: t.cpuPct, MemoryBytes: t.rss}
		}
	}
	return out
}

func (s *Sampler) loop() {
	defer crashguard.Guard()
	for {
		time.Sleep(s.interval())
		if !s.sampleOnce() {
			return
		}
	}
}

// interval is fast while any run is young, so a short run is still caught.
func (s *Sampler) interval() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for _, t := range s.runs {
		if now.Sub(t.started) < youngFor {
			return s.fast
		}
	}
	return slowInterval
}

// sampleOnce reads every tracked group and reports whether the loop should
// keep going. The read happens outside the lock so a slow /proc scan or ps
// never blocks Track or the usage reads.
func (s *Sampler) sampleOnce() bool {
	s.mu.Lock()
	if len(s.runs) == 0 {
		s.running = false
		s.mu.Unlock()
		return false
	}
	pgids := make(map[int]struct{}, len(s.runs))
	for _, t := range s.runs {
		pgids[t.pgid] = struct{}{}
	}
	s.mu.Unlock()

	stats := s.read(pgids)
	now := s.now()

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.runs {
		g, ok := stats[t.pgid]
		if !ok {
			continue
		}
		if t.sampled {
			// A reaped child takes its CPU time out of the live sum, so the
			// delta can go negative; that tick reads as idle.
			if dt := now.Sub(t.prevAt); dt > 0 {
				t.cpuPct = max(0, float64(g.CPU-t.prevCPU)/float64(dt)*100)
			}
		}
		t.sampled = true
		t.prevCPU, t.prevAt = g.CPU, now
		t.rss = g.RSS
		t.peak = max(t.peak, g.Peak, g.RSS)
	}
	return true
}
