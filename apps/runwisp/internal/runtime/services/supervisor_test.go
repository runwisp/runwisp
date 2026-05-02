// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package services

import (
	"testing"
	"time"

	"github.com/runwisp/runwisp/internal/runtime/retry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSupervisorClampsInstances(t *testing.T) {
	for _, raw := range []int{-3, 0, 1} {
		s := NewSupervisor("svc", raw)
		assert.GreaterOrEqualf(t, s.Instances(), 1, "instances input %d should clamp to >=1", raw)
	}
	s := NewSupervisor("svc", 5)
	assert.Equal(t, 5, s.Instances())
}

func TestReserveAutoAssignPicksLowestFreeIndex(t *testing.T) {
	s := NewSupervisor("svc", 3)
	idx, err := s.Reserve(nil)
	require.NoError(t, err)
	assert.Equal(t, 0, idx)
	idx, err = s.Reserve(nil)
	require.NoError(t, err)
	assert.Equal(t, 1, idx)
}

func TestReserveAutoAssignSkipsTakenSlots(t *testing.T) {
	s := NewSupervisor("svc", 3)
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
	s := NewSupervisor("svc", 2)
	_, err := s.Reserve(nil)
	require.NoError(t, err)
	_, err = s.Reserve(nil)
	require.NoError(t, err)
	_, err = s.Reserve(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no free replica slots")
}

func TestReservePinnedIndexOutOfRange(t *testing.T) {
	s := NewSupervisor("svc", 3)
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
	s := NewSupervisor("svc", 3)
	taken := 1
	_, err := s.Reserve(&taken)
	require.NoError(t, err)
	_, err = s.Reserve(&taken)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already live")
}

func TestReserveLimitBelowOneDefaultsToOne(t *testing.T) {
	s := NewSupervisor("svc", 0)
	idx, err := s.Reserve(nil)
	require.NoError(t, err)
	assert.Equal(t, 0, idx)
	_, err = s.Reserve(nil)
	require.Error(t, err, "second slot must fail when limit is 1")
}

func TestRecordExitIncrementsCounter(t *testing.T) {
	s := NewSupervisor("svc", 1)
	_, err := s.Reserve(nil)
	require.NoError(t, err)

	// Quick exits well under the reset threshold accumulate.
	first := s.RecordExit(0, time.Millisecond)
	assert.Equal(t, 0, first, "first exit returns the pre-increment value")

	_, err = s.Reserve(nil)
	require.NoError(t, err)
	second := s.RecordExit(0, time.Millisecond)
	assert.Equal(t, 1, second, "second exit returns 1")

	assert.Equal(t, 2, s.Attempts(0))
}

func TestRecordExitResetsCounterAfterHealthyRun(t *testing.T) {
	s := NewSupervisor("svc", 1)

	// Build up some attempts via two quick exits.
	for i := 0; i < 2; i++ {
		_, err := s.Reserve(nil)
		require.NoError(t, err)
		s.RecordExit(0, time.Millisecond)
	}
	require.Equal(t, 2, s.Attempts(0))

	// A run that lasted past the reset threshold zeroes the counter before
	// reading it: the next restart should start over.
	_, err := s.Reserve(nil)
	require.NoError(t, err)
	next := s.RecordExit(0, retry.BackoffResetThreshold)
	assert.Equal(t, 0, next, "post-healthy exit returns 0 (base delay)")
	assert.Equal(t, 1, s.Attempts(0))
}

func TestRecordExitReleasesSlot(t *testing.T) {
	s := NewSupervisor("svc", 1)
	_, err := s.Reserve(nil)
	require.NoError(t, err)
	require.True(t, s.IsLive(0))

	s.RecordExit(0, time.Millisecond)
	assert.False(t, s.IsLive(0))
}

func TestMissingSlots(t *testing.T) {
	s := NewSupervisor("svc", 4)
	pin1 := 1
	_, err := s.Reserve(&pin1)
	require.NoError(t, err)

	missing := s.MissingSlots()
	assert.Equal(t, []int{0, 2, 3}, missing)
}

func TestSetInstancesAffectsSubsequentReserve(t *testing.T) {
	s := NewSupervisor("svc", 1)
	_, err := s.Reserve(nil)
	require.NoError(t, err)
	_, err = s.Reserve(nil)
	require.Error(t, err)

	s.SetInstances(3)
	idx, err := s.Reserve(nil)
	require.NoError(t, err)
	assert.Equal(t, 1, idx)
}
