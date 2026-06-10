// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

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

// Supervisor tracks the live instance slots for one service task and the
// consecutive-restart attempt counter per slot.
type Supervisor struct {
	taskName     string
	instances    int
	live         map[int]struct{}
	attempts     map[int]int
	healthyAfter time.Duration
	stopped      bool

	// liveSince records when each slot reached the running phase. A slot is
	// only entered here by MarkLive (at PhaseRunning, not at reservation) and
	// dropped in RecordExit, so its keys are exactly the currently-running
	// slots. IsHealthy uses it to answer the live "is it up yet?" question that
	// readiness gating needs — distinct from the retrospective healthy_after
	// check RecordExit performs after an exit.
	liveSince map[int]time.Time
	// clock supplies "now" for liveSince math so IsHealthy needs no argument
	// and tests can pin time. Defaults to time.Now when nil.
	clock func() time.Time

	// startFails counts consecutive fast failures per slot — exits that
	// happened before the instance reached healthy_after of uptime. A healthy
	// run (or a clean, non-failure exit) resets it; exceeding start_retries
	// trips the slot into the FATAL state.
	startFails map[int]int
	// fatal flags slots that exhausted their start-retry budget. A FATAL slot
	// is not refilled until an operator restart (or daemon restart) clears it.
	fatal map[int]bool
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
		live:         make(map[int]struct{}),
		attempts:     make(map[int]int),
		healthyAfter: healthyAfter,
		stopped:      startStopped,
		liveSince:    make(map[int]time.Time),
		clock:        clock,
		startFails:   make(map[int]int),
		fatal:        make(map[int]bool),
	}
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
		if _, taken := s.live[idx]; taken {
			return 0, fmt.Errorf("instance %d already live for service %s", idx, s.taskName)
		}
		s.live[idx] = struct{}{}
		return idx, nil
	}
	for i := 0; i < s.instances; i++ {
		if _, taken := s.live[i]; !taken {
			s.live[i] = struct{}{}
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
	delete(s.live, idx)
	delete(s.liveSince, idx)

	if runDuration >= s.healthyAfter {
		s.attempts[idx] = 0
	}
	nextAttempt = s.attempts[idx]
	s.attempts[idx] = nextAttempt + 1

	if !wasFailure || runDuration >= s.healthyAfter {
		s.startFails[idx] = 0
		delete(s.fatal, idx)
		return nextAttempt, false
	}
	s.startFails[idx]++
	if s.startFails[idx] > startRetries {
		s.fatal[idx] = true
		return nextAttempt, true
	}
	return nextAttempt, false
}

// IsLive reports whether a given instance index is currently occupied.
func (s *Supervisor) IsLive(idx int) bool {
	_, ok := s.live[idx]
	return ok
}

// MarkLive stamps the moment a slot reached the running phase. The runtime
// calls it once per run, when the run transitions to PhaseRunning — that is the
// reference point for the live readiness gate, distinct from the reservation
// recorded by Reserve.
func (s *Supervisor) MarkLive(idx int) {
	s.liveSince[idx] = s.clock()
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
	for idx, since := range s.liveSince {
		if s.fatal[idx] {
			continue
		}
		if now.Sub(since) >= s.healthyAfter {
			return true
		}
	}
	return false
}

// LiveCount returns the number of currently occupied instance slots.
func (s *Supervisor) LiveCount() int { return len(s.live) }

// MissingSlots returns the indexes in [0, instances) that are not currently
// live. Used by StartServiceInstances to bring a service up to desired count.
func (s *Supervisor) MissingSlots() []int {
	missing := make([]int, 0, s.instances)
	for i := 0; i < s.instances; i++ {
		if _, taken := s.live[i]; !taken {
			missing = append(missing, i)
		}
	}
	return missing
}

// Attempts returns the current consecutive-restart counter for a slot. This
// is the post-RecordExit value (the number of exits observed for the slot
// since the last healthy run); exposed for tests and observability.
func (s *Supervisor) Attempts(idx int) int { return s.attempts[idx] }

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
func (s *Supervisor) StartFails(idx int) int { return s.startFails[idx] }

// IsFatal reports whether a specific instance slot has exhausted its
// start-retry budget and been given up on.
func (s *Supervisor) IsFatal(idx int) bool { return s.fatal[idx] }

// IsAnyFatal reports whether any instance slot is in the FATAL state.
func (s *Supervisor) IsAnyFatal() bool { return len(s.fatal) > 0 }

// ClearFatal drops every FATAL flag and the fast-failure streak that produced
// it, so the affected slots can be refilled with a fresh start-retry budget.
func (s *Supervisor) ClearFatal() {
	for idx := range s.fatal {
		delete(s.fatal, idx)
		s.startFails[idx] = 0
	}
}

func clampInstances(n int) int {
	if n < 1 {
		return 1
	}
	return n
}
