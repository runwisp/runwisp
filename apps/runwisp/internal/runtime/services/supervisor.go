// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package services owns per-task supervisor state for service kinds: which
// replica slots are currently live and how many consecutive short-lived
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

// defaultBackoffReset is the fallback "healthy run" threshold when a caller
// constructs a Supervisor without a configured value. The config layer
// applies a default during load; this only protects direct test usage.
const defaultBackoffReset = 60 * time.Second

// Supervisor tracks the live replica slots for one service task and the
// consecutive-restart attempt counter per slot.
type Supervisor struct {
	taskName     string
	instances    int
	live         map[int]struct{}
	attempts     map[int]int
	backoffReset time.Duration
}

// NewSupervisor creates a Supervisor for a service with the given desired
// replica count. instances < 1 is normalised to 1. backoffReset is the
// minimum live duration that resets a replica's consecutive-restart counter;
// non-positive values fall back to the package default.
func NewSupervisor(taskName string, instances int, backoffReset time.Duration) *Supervisor {
	if backoffReset <= 0 {
		backoffReset = defaultBackoffReset
	}
	return &Supervisor{
		taskName:     taskName,
		instances:    clampInstances(instances),
		live:         make(map[int]struct{}),
		attempts:     make(map[int]int),
		backoffReset: backoffReset,
	}
}

// SetInstances updates the desired replica count. Slots that fall outside the
// new range remain live until they exit; the supervisor simply won't hand
// them out again.
func (s *Supervisor) SetInstances(instances int) {
	s.instances = clampInstances(instances)
}

// SetBackoffReset updates the "run was healthy" threshold for restart
// counter resets. A non-positive value reverts to the package default.
func (s *Supervisor) SetBackoffReset(backoffReset time.Duration) {
	if backoffReset <= 0 {
		backoffReset = defaultBackoffReset
	}
	s.backoffReset = backoffReset
}

// Instances returns the configured replica count (always >= 1).
func (s *Supervisor) Instances() int { return s.instances }

// Reserve allocates a replica slot. If requested is non-nil, that specific
// index is pinned (used for restarts of a known replica); otherwise the
// lowest free slot is returned.
func (s *Supervisor) Reserve(requested *int) (int, error) {
	if requested != nil {
		idx := *requested
		if idx < 0 || idx >= s.instances {
			return 0, fmt.Errorf("replica index %d out of range [0,%d) for service %s", idx, s.instances, s.taskName)
		}
		if _, taken := s.live[idx]; taken {
			return 0, fmt.Errorf("replica %d already live for service %s", idx, s.taskName)
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
	return 0, fmt.Errorf("no free replica slots for service %s (instances=%d)", s.taskName, s.instances)
}

// RecordExit releases a replica slot and advances the consecutive-restart
// counter for that slot. The returned int is the attempt index to feed into
// retry.ComputeRestartDelay for the *next* restart (0 means "first restart in
// this backoff cycle"). Runs that lasted at least the supervisor's configured
// backoff_reset_after reset the counter before the return value is captured.
func (s *Supervisor) RecordExit(idx int, runDuration time.Duration) int {
	delete(s.live, idx)
	if runDuration >= s.backoffReset {
		s.attempts[idx] = 0
	}
	next := s.attempts[idx]
	s.attempts[idx] = next + 1
	return next
}

// IsLive reports whether a given replica index is currently occupied.
func (s *Supervisor) IsLive(idx int) bool {
	_, ok := s.live[idx]
	return ok
}

// LiveCount returns the number of currently occupied replica slots.
func (s *Supervisor) LiveCount() int { return len(s.live) }

// MissingSlots returns the indexes in [0, instances) that are not currently
// live. Used by StartServiceReplicas to bring a service up to desired count.
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

func clampInstances(n int) int {
	if n < 1 {
		return 1
	}
	return n
}
