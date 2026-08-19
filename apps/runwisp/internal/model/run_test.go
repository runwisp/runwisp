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
	assert.Equal(t, "success", r.DisplayStatus())
}

func TestRun_DisplayStatus_EndedNilReason(t *testing.T) {
	r := &Run{Status: PhaseEnded, EndReason: nil}
	assert.Equal(t, "stopped", r.DisplayStatus())
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
