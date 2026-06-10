// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package services

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSupervisorForTest(name string, instances int) *Supervisor {
	return NewSupervisor(name, instances, 0, false, nil)
}

// recordExitBackoff drives RecordExit for the restart-backoff tests: each exit
// is a failure, but start_retries is set high enough that FATAL never trips, so
// the assertions isolate the backoff counter. Returns the next restart attempt.
func recordExitBackoff(s *Supervisor, idx int, runDuration time.Duration) int {
	const neverFatal = 1_000_000
	next, _ := s.RecordExit(idx, runDuration, neverFatal, true)
	return next
}

func TestNewSupervisorClampsInstances(t *testing.T) {
	for _, raw := range []int{-3, 0, 1} {
		s := newSupervisorForTest("svc", raw)
		assert.GreaterOrEqualf(t, s.Instances(), 1, "instances input %d should clamp to >=1", raw)
	}
	s := newSupervisorForTest("svc", 5)
	assert.Equal(t, 5, s.Instances())
}

func TestReserveAutoAssignPicksLowestFreeIndex(t *testing.T) {
	s := newSupervisorForTest("svc", 3)
	idx, err := s.Reserve(nil)
	require.NoError(t, err)
	assert.Equal(t, 0, idx)
	idx, err = s.Reserve(nil)
	require.NoError(t, err)
	assert.Equal(t, 1, idx)
}

func TestReserveAutoAssignSkipsTakenSlots(t *testing.T) {
	s := newSupervisorForTest("svc", 3)
	pin0, pin2 := 0, 2
	_, err := s.Reserve(&pin0)
	require.NoError(t, err)
	_, err = s.Reserve(&pin2)
	require.NoError(t, err)

	idx, err := s.Reserve(nil)
	require.NoError(t, err)
	assert.Equal(t, 1, idx, "should fill the gap at index 1")
}

func TestReserveAutoAssignFailsWhenFull(t *testing.T) {
	s := newSupervisorForTest("svc", 2)
	_, err := s.Reserve(nil)
	require.NoError(t, err)
	_, err = s.Reserve(nil)
	require.NoError(t, err)
	_, err = s.Reserve(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no free instance slots")
}

func TestReservePinnedIndexOutOfRange(t *testing.T) {
	s := newSupervisorForTest("svc", 3)
	bad := 5
	_, err := s.Reserve(&bad)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")

	neg := -1
	_, err = s.Reserve(&neg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}

func TestReservePinnedIndexAlreadyTaken(t *testing.T) {
	s := newSupervisorForTest("svc", 3)
	taken := 1
	_, err := s.Reserve(&taken)
	require.NoError(t, err)
	_, err = s.Reserve(&taken)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already live")
}

func TestReserveLimitBelowOneDefaultsToOne(t *testing.T) {
	s := newSupervisorForTest("svc", 0)
	idx, err := s.Reserve(nil)
	require.NoError(t, err)
	assert.Equal(t, 0, idx)
	_, err = s.Reserve(nil)
	require.Error(t, err, "second slot must fail when limit is 1")
}

func TestRecordExitIncrementsCounter(t *testing.T) {
	s := newSupervisorForTest("svc", 1)
	_, err := s.Reserve(nil)
	require.NoError(t, err)

	// Quick exits well under the reset threshold accumulate.
	first := recordExitBackoff(s, 0, time.Millisecond)
	assert.Equal(t, 0, first, "first exit returns the pre-increment value")

	_, err = s.Reserve(nil)
	require.NoError(t, err)
	second := recordExitBackoff(s, 0, time.Millisecond)
	assert.Equal(t, 1, second, "second exit returns 1")

	assert.Equal(t, 2, s.Attempts(0))
}

func TestNewSupervisorStartStopped(t *testing.T) {
	s := NewSupervisor("svc", 2, 0, true, nil)
	assert.True(t, s.IsStopped(), "startStopped=true should boot in the stopped state")

	s.MarkRunning()
	assert.False(t, s.IsStopped(), "MarkRunning clears the stopped flag")

	running := NewSupervisor("svc", 2, 0, false, nil)
	assert.False(t, running.IsStopped(), "startStopped=false boots running")
}

func TestRecordExitResetsCounterAfterHealthyRun(t *testing.T) {
	const threshold = 50 * time.Millisecond
	s := NewSupervisor("svc", 1, threshold, false, nil)

	// Build up some attempts via two quick exits.
	for i := 0; i < 2; i++ {
		_, err := s.Reserve(nil)
		require.NoError(t, err)
		recordExitBackoff(s, 0, time.Millisecond)
	}
	require.Equal(t, 2, s.Attempts(0))

	// A run that lasted past the configured threshold zeroes the counter
	// before reading it: the next restart should start over.
	_, err := s.Reserve(nil)
	require.NoError(t, err)
	next := recordExitBackoff(s, 0, threshold)
	assert.Equal(t, 0, next, "post-healthy exit returns 0 (base delay)")
	assert.Equal(t, 1, s.Attempts(0))
}

func TestSetHealthyAfterTakesEffect(t *testing.T) {
	const lowThreshold = 5 * time.Millisecond
	s := NewSupervisor("svc", 1, time.Hour, false, nil)

	_, err := s.Reserve(nil)
	require.NoError(t, err)
	// Brief run under the original 1-hour threshold accumulates.
	first := recordExitBackoff(s, 0, lowThreshold)
	assert.Equal(t, 0, first)
	require.Equal(t, 1, s.Attempts(0))

	// Lowering the threshold means the same brief run now counts as healthy.
	s.SetHealthyAfter(lowThreshold)
	_, err = s.Reserve(nil)
	require.NoError(t, err)
	next := recordExitBackoff(s, 0, lowThreshold)
	assert.Equal(t, 0, next, "lowered threshold should make this exit count as healthy")
}

func TestRecordExitReleasesSlot(t *testing.T) {
	s := newSupervisorForTest("svc", 1)
	_, err := s.Reserve(nil)
	require.NoError(t, err)
	require.True(t, s.IsLive(0))

	recordExitBackoff(s, 0, time.Millisecond)
	assert.False(t, s.IsLive(0))
}

func TestMissingSlots(t *testing.T) {
	s := newSupervisorForTest("svc", 4)
	pin1 := 1
	_, err := s.Reserve(&pin1)
	require.NoError(t, err)

	missing := s.MissingSlots()
	assert.Equal(t, []int{0, 2, 3}, missing)
}

func TestIsHealthyTracksLiveUptime(t *testing.T) {
	const threshold = 30 * time.Second
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	s := NewSupervisor("svc", 2, threshold, false, clock)

	require.False(t, s.IsHealthy(), "no live instance yet")

	_, err := s.Reserve(nil)
	require.NoError(t, err)
	s.MarkLive(0)
	assert.False(t, s.IsHealthy(), "just-live instance has not reached the threshold")

	now = now.Add(threshold - time.Second)
	assert.False(t, s.IsHealthy(), "still one second short of healthy_after")

	now = now.Add(time.Second)
	assert.True(t, s.IsHealthy(), "instance has now been live >= healthy_after")

	// The exit drops the slot from liveSince, so health falls back to false.
	s.RecordExit(0, threshold, 0, false)
	assert.False(t, s.IsHealthy(), "no live instance after exit")
}

func TestIsHealthyFalseWhenStopped(t *testing.T) {
	const threshold = 10 * time.Second
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	s := NewSupervisor("svc", 1, threshold, false, clock)

	_, err := s.Reserve(nil)
	require.NoError(t, err)
	s.MarkLive(0)
	now = now.Add(threshold)
	require.True(t, s.IsHealthy())

	s.MarkStopped()
	assert.False(t, s.IsHealthy(), "an operator-stopped service is never healthy")
}

func TestIsHealthyIgnoresFatalSlot(t *testing.T) {
	const threshold = 10 * time.Second
	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	s := NewSupervisor("svc", 2, threshold, false, clock)

	// Slot 0 trips FATAL via a fast failure; slot 1 stays a fresh reservation.
	_, err := s.Reserve(nil)
	require.NoError(t, err)
	s.MarkLive(0)
	_, fatal := s.RecordExit(0, time.Millisecond, 0, true)
	require.True(t, fatal)

	assert.False(t, s.IsHealthy(), "a FATAL slot does not make the service healthy")
}

func TestSetInstancesAffectsSubsequentReserve(t *testing.T) {
	s := newSupervisorForTest("svc", 1)
	_, err := s.Reserve(nil)
	require.NoError(t, err)
	_, err = s.Reserve(nil)
	require.Error(t, err)

	s.SetInstances(3)
	idx, err := s.Reserve(nil)
	require.NoError(t, err)
	assert.Equal(t, 1, idx)
}
