// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package services

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFatalSupervisor builds a supervisor whose healthy_after window is huge, so
// every brief exit counts as a fast failure and the FATAL mechanic is the only
// thing under test.
func newFatalSupervisor(instances int) *Supervisor {
	return NewSupervisor("svc", instances, time.Hour, false)
}

func TestRecordExitTripsFatalAfterStartRetries(t *testing.T) {
	const startRetries = 2
	s := newFatalSupervisor(1)

	// The first start_retries fast failures are tolerated.
	for i := 1; i <= startRetries; i++ {
		_, fatal := s.RecordExit(0, time.Millisecond, startRetries, true)
		require.Falsef(t, fatal, "fast failure %d should not be FATAL yet", i)
		require.False(t, s.IsFatal(0))
	}

	// The (start_retries+1)th consecutive fast failure trips FATAL.
	_, fatal := s.RecordExit(0, time.Millisecond, startRetries, true)
	require.True(t, fatal, "exceeding start_retries must trip FATAL")
	assert.True(t, s.IsFatal(0))
	assert.True(t, s.IsAnyFatal())
	assert.Equal(t, startRetries+1, s.StartFails(0))
}

func TestRecordExitHealthyRunClearsStartFails(t *testing.T) {
	const startRetries = 3
	s := newFatalSupervisor(1)

	s.RecordExit(0, time.Millisecond, startRetries, true)
	s.RecordExit(0, time.Millisecond, startRetries, true)
	require.Equal(t, 2, s.StartFails(0))

	// A run that reaches healthy_after is a successful start: the streak resets,
	// so a service that flaps then settles never goes FATAL.
	_, fatal := s.RecordExit(0, time.Hour, startRetries, true)
	assert.False(t, fatal)
	assert.Equal(t, 0, s.StartFails(0))
	assert.False(t, s.IsFatal(0))
}

func TestRecordExitNonFailureNeverFatal(t *testing.T) {
	// start_retries=0 would trip FATAL on the first *failure*; a clean exit must
	// not — this is the exit_codes tie-in (a success-listed fast exit is healthy).
	s := newFatalSupervisor(1)
	for i := 0; i < 5; i++ {
		_, fatal := s.RecordExit(0, time.Millisecond, 0, false)
		require.False(t, fatal)
	}
	assert.False(t, s.IsFatal(0))
	assert.Equal(t, 0, s.StartFails(0))
}

func TestClearFatalRecovers(t *testing.T) {
	s := newFatalSupervisor(1)
	_, fatal := s.RecordExit(0, time.Millisecond, 0, true)
	require.True(t, fatal)
	require.True(t, s.IsFatal(0))

	s.ClearFatal()
	assert.False(t, s.IsFatal(0))
	assert.False(t, s.IsAnyFatal())
	assert.Equal(t, 0, s.StartFails(0))
}

func TestMarkRunningClearsFatal(t *testing.T) {
	s := newFatalSupervisor(1)
	_, fatal := s.RecordExit(0, time.Millisecond, 0, true)
	require.True(t, fatal)

	// An operator restart routes through MarkRunning — it must reset the budget.
	s.MarkRunning()
	assert.False(t, s.IsFatal(0))
	assert.False(t, s.IsAnyFatal())
}

func TestFatalIsPerInstance(t *testing.T) {
	s := newFatalSupervisor(2)
	_, fatal := s.RecordExit(0, time.Millisecond, 0, true)
	require.True(t, fatal)

	assert.True(t, s.IsFatal(0))
	assert.False(t, s.IsFatal(1), "a sibling instance is unaffected")
	assert.True(t, s.IsAnyFatal())
}
