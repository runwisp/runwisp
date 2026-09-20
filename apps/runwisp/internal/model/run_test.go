// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun_DisplayStatus_Running(t *testing.T) {
	r := &Run{Status: PhaseRunning}
	assert.Equal(t, "running", r.DisplayStatus())
}

func TestRun_DisplayStatus_EndedSuccess(t *testing.T) {
	reason := ReasonSuccess
	r := &Run{Status: PhaseEnded, EndReason: &reason}
	assert.Equal(t, "succeeded", r.DisplayStatus())
}

func TestRun_DisplayStatus_EndedNilReason(t *testing.T) {
	r := &Run{Status: PhaseEnded, EndReason: nil}
	assert.Equal(t, "stopped", r.DisplayStatus())
}

// TestRun_IsRetryable_NeverExecutedReasonsAreExcluded checks that every
// end-reason meaning "the run never actually executed" is excluded from
// retry, not just ReasonSkipped and ReasonMissed. ReasonQueueFull belongs to
// the same class: the queue overflow policy rejected the firing before it
// ever ran, so there is no failed attempt to retry.
func TestRun_IsRetryable_NeverExecutedReasonsAreExcluded(t *testing.T) {
	tests := []struct {
		name   string
		reason EndReason
		want   bool
	}{
		{"success is not retryable", ReasonSuccess, false},
		{"skipped is not retryable", ReasonSkipped, false},
		{"missed is not retryable", ReasonMissed, false},
		{"queue_full is not retryable", ReasonQueueFull, false},
		{"failed is retryable", ReasonFailed, true},
		{"crashed is retryable", ReasonCrashed, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Run{Status: PhaseEnded, EndReason: &tt.reason}
			assert.Equal(t, tt.want, r.IsRetryable())
		})
	}
}

func TestRun_Copy_PointerFieldsAreIndependent(t *testing.T) {
	extID := "ext-123"
	reason := ReasonFailed
	now := time.Now()
	end := now.Add(time.Second)
	retryID := "retry-abc"

	orig := &Run{
		ID:           "run-1",
		ExecutionID:  &extID,
		EndReason:    &reason,
		StartedAt:    &now,
		EndedAt:      &end,
		RetryOfRunID: &retryID,
	}

	cpy := orig.Copy()
	require.NotNil(t, cpy)

	assert.NotSame(t, orig.ExecutionID, cpy.ExecutionID)
	assert.Equal(t, extID, *cpy.ExecutionID)

	assert.NotSame(t, orig.EndReason, cpy.EndReason)
	assert.Equal(t, reason, *cpy.EndReason)

	assert.NotSame(t, orig.StartedAt, cpy.StartedAt)
	assert.Equal(t, now, *cpy.StartedAt)

	assert.NotSame(t, orig.EndedAt, cpy.EndedAt)
	assert.Equal(t, end, *cpy.EndedAt)

	assert.NotSame(t, orig.RetryOfRunID, cpy.RetryOfRunID)
	assert.Equal(t, retryID, *cpy.RetryOfRunID)
}
