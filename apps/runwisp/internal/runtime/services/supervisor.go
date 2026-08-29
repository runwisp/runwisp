// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package services owns per-task supervisor state for service kinds: which
// instance slots are currently live and how many consecutive short-lived
// exits each slot has accumulated.
//
// Supervisor is NOT internally synchronised. Callers (the runtime task
// manager) hold their own mutex and call into the supervisor under it. This
// keeps the lock-ordering invariants centralised in the manager and avoids
// the need to reason about nested locks.
package services

import (
	"fmt"
	"time"
)

// defaultHealthyAfter is the fallback "healthy run" threshold when a caller
// constructs a Supervisor without a configured value. The config layer
// applies a default during load; this only protects direct test usage.
const defaultHealthyAfter = 60 * time.Second

// slotState is one instance slot's state: whether it's currently occupied,
// its consecutive-restart attempt counter, when it last reached the running
// phase, its consecutive fast-failure streak, and whether it has tripped into
// the FATAL state. Keyed by slot index on Supervisor.slots so all five facets
// of a slot move together instead of four parallel maps updated in lockstep.
type slotState struct {
	live       bool
	attempts   int
	liveSince  time.Time
	startFails int
	fatal      bool
}

// Supervisor tracks the live instance slots for one service task and the
// consecutive-restart attempt counter per slot.
type Supervisor struct {
	taskName     string
	instances    int
	slots        map[int]*slotState
	healthyAfter time.Duration
	stopped      bool

	// clock supplies "now" for liveSince math so IsHealthy needs no argument
	// and tests can pin time. Defaults to time.Now when nil.
	clock func() time.Time
}

// NewSupervisor creates a Supervisor for a service with the given desired
// instance count. instances < 1 is normalised to 1. healthyAfter is the
// minimum live duration that marks an instance as healthy — it both resets the
// consecutive-restart counter and clears the failed-start streak; non-positive
// values fall back to the package default. startStopped seeds the operator-stop
// flag so an autostart=false service boots without spawning instances until an
// operator starts it. clock supplies "now" for the live-readiness signal; a nil
// clock falls back to time.Now.
func NewSupervisor(taskName string, instances int, healthyAfter time.Duration, startStopped bool, clock func() time.Time) *Supervisor {
	if healthyAfter <= 0 {
		healthyAfter = defaultHealthyAfter
	}
	if clock == nil {
		clock = time.Now
	}
	return &Supervisor{
		taskName:     taskName,
		instances:    clampInstances(instances),
		slots:        make(map[int]*slotState),
		healthyAfter: healthyAfter,
		stopped:      startStopped,
		clock:        clock,
	}
}

// slot returns a read-only snapshot of a slot's state, or the zero value if
// the slot has never been touched. Never allocates a map entry.
func (s *Supervisor) slot(idx int) slotState {
	if st, ok := s.slots[idx]; ok {
		return *st
	}
	return slotState{}
}

// mutateSlot returns the slot's state for in-place mutation, allocating an
// entry on first touch.
func (s *Supervisor) mutateSlot(idx int) *slotState {
	st, ok := s.slots[idx]
	if !ok {
		st = &slotState{}
		s.slots[idx] = st
	}
	return st
}

// SetInstances updates the desired instance count. Slots that fall outside the
// new range remain live until they exit; the supervisor simply won't hand
// them out again.
func (s *Supervisor) SetInstances(instances int) {
	s.instances = clampInstances(instances)
}

// SetHealthyAfter updates the "run was healthy" threshold that drives both the
// restart-counter reset and the failed-start streak. A non-positive value
// reverts to the package default.
func (s *Supervisor) SetHealthyAfter(healthyAfter time.Duration) {
	if healthyAfter <= 0 {
		healthyAfter = defaultHealthyAfter
	}
	s.healthyAfter = healthyAfter
}

// Instances returns the configured instance count (always >= 1).
func (s *Supervisor) Instances() int { return s.instances }

// Reserve allocates an instance slot. If requested is non-nil, that specific
// index is pinned (used for restarts of a known instance); otherwise the
// lowest free slot is returned.
func (s *Supervisor) Reserve(requested *int) (int, error) {
	if requested != nil {
		idx := *requested
		if idx < 0 || idx >= s.instances {
			return 0, fmt.Errorf("instance index %d out of range [0,%d) for service %s", idx, s.instances, s.taskName)
		}
		if s.slot(idx).live {
			return 0, fmt.Errorf("instance %d already live for service %s", idx, s.taskName)
		}
		s.mutateSlot(idx).live = true
		return idx, nil
	}
	for i := 0; i < s.instances; i++ {
		if !s.slot(i).live {
			s.mutateSlot(i).live = true
			return i, nil
		}
	}
	return 0, fmt.Errorf("no free instance slots for service %s (instances=%d)", s.taskName, s.instances)
}

// RecordExit releases an instance slot, advances its restart-backoff counter,
// and tracks consecutive fast failures toward the FATAL threshold.
//
// The returned nextAttempt is the index to feed into retry.ComputeRestartDelay
// for the next restart (0 means "first restart in this backoff cycle"). Runs
// that lasted at least the supervisor's configured healthy_after reset that
// counter before the value is captured.
//
// fatal reports whether this exit tripped the slot into the FATAL state: the
// instance fast-failed (wasFailure, before reaching healthy_after of uptime)
// more than startRetries times in a row. A FATAL slot is left empty — the
// caller must not restart it. A healthy run (reached healthy_after) or any
// non-failure exit clears the fast-failure streak and any prior FATAL flag.
func (s *Supervisor) RecordExit(idx int, runDuration time.Duration, startRetries int, wasFailure bool) (nextAttempt int, fatal bool) {
	st := s.mutateSlot(idx)
	st.live = false
	st.liveSince = time.Time{}

	if runDuration >= s.healthyAfter {
		st.attempts = 0
	}
	nextAttempt = st.attempts
	st.attempts = nextAttempt + 1

	if !wasFailure || runDuration >= s.healthyAfter {
		st.startFails = 0
		st.fatal = false
		return nextAttempt, false
	}
	st.startFails++
	if st.startFails > startRetries {
		st.fatal = true
		return nextAttempt, true
	}
	return nextAttempt, false
}

// IsLive reports whether a given instance index is currently occupied.
func (s *Supervisor) IsLive(idx int) bool {
	return s.slot(idx).live
}

// MarkLive stamps the moment a slot reached the running phase. The runtime
// calls it once per run, when the run transitions to PhaseRunning — that is the
// reference point for the live readiness gate, distinct from the reservation
// recorded by Reserve.
func (s *Supervisor) MarkLive(idx int) {
	s.mutateSlot(idx).liveSince = s.clock()
}

// IsHealthy reports whether the service currently has at least one instance
// that has been running continuously for at least healthy_after. This is the
// live readiness signal a dependent waits on at boot. It is false while the
// service is operator-stopped and ignores FATAL slots (they are not coming
// back without an operator restart).
func (s *Supervisor) IsHealthy() bool {
	if s.stopped {
		return false
	}
	now := s.clock()
	for _, st := range s.slots {
		if st.liveSince.IsZero() || st.fatal {
			continue
		}
		if now.Sub(st.liveSince) >= s.healthyAfter {
			return true
		}
	}
	return false
}

// LiveCount returns the number of currently occupied instance slots.
func (s *Supervisor) LiveCount() int {
	n := 0
	for _, st := range s.slots {
		if st.live {
			n++
		}
	}
	return n
}

// MissingSlots returns the indexes in [0, instances) that are not currently
// live and not FATAL. Used by StartServiceInstances to bring a service up to
// desired count. A FATAL slot is deliberately excluded: it has exhausted its
// start-retry budget and only comes back once something clears the flag
// (MarkRunning/ClearFatal) — treating it as "missing" would silently retry a
// slot the supervisor already gave up on.
func (s *Supervisor) MissingSlots() []int {
	missing := make([]int, 0, s.instances)
	for i := 0; i < s.instances; i++ {
		st := s.slot(i)
		if !st.live && !st.fatal {
			missing = append(missing, i)
		}
	}
	return missing
}

// Attempts returns the current consecutive-restart counter for a slot. This
// is the post-RecordExit value (the number of exits observed for the slot
// since the last healthy run); exposed for tests and observability.
func (s *Supervisor) Attempts(idx int) int { return s.slot(idx).attempts }

// MarkStopped flags the service as operator-stopped. The supervisor will
// refuse to spawn new instances until MarkRunning clears the flag. The flag
// lives only for the daemon's lifetime; a restart resumes the service.
func (s *Supervisor) MarkStopped() { s.stopped = true }

// MarkRunning clears the operator-stop flag so the supervisor can refill
// instance slots again. It also clears any FATAL flags: an operator bringing
// the service back up is an explicit "try again" that resets the start-retry
// budget for every slot.
func (s *Supervisor) MarkRunning() {
	s.stopped = false
	s.ClearFatal()
}

// IsStopped reports whether an operator has stopped the service.
func (s *Supervisor) IsStopped() bool { return s.stopped }

// StartFails returns the current consecutive fast-failure count for a slot —
// the number of below-healthy_after exits since its last healthy run. Exposed
// for the FATAL event payload and tests.
func (s *Supervisor) StartFails(idx int) int { return s.slot(idx).startFails }

// IsFatal reports whether a specific instance slot has exhausted its
// start-retry budget and been given up on.
func (s *Supervisor) IsFatal(idx int) bool { return s.slot(idx).fatal }

// IsAnyFatal reports whether any instance slot is in the FATAL state.
func (s *Supervisor) IsAnyFatal() bool {
	for _, st := range s.slots {
		if st.fatal {
			return true
		}
	}
	return false
}

// ClearFatal drops every FATAL flag and the fast-failure streak that produced
// it, so the affected slots can be refilled with a fresh start-retry budget.
func (s *Supervisor) ClearFatal() {
	for _, st := range s.slots {
		if st.fatal {
			st.fatal = false
			st.startFails = 0
		}
	}
}

func clampInstances(n int) int {
	if n < 1 {
		return 1
	}
	return n
}
