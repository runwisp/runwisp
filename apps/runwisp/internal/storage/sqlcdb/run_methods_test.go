// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package sqlcdb

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
		ID:                  "run-1",
		ExternalExecutionID: &extID,
		EndReason:           &reason,
		StartAt:             &now,
		EndAt:               &end,
		RetryOfRunID:        &retryID,
	}

	cpy := orig.Copy()
	require.NotNil(t, cpy)

	// Every nullable pointer field must have been deep-copied: the copy's
	// pointer addresses must differ from the original's. The values they
	// point to must match.
	assert.NotSame(t, orig.ExternalExecutionID, cpy.ExternalExecutionID)
	assert.Equal(t, extID, *cpy.ExternalExecutionID)

	assert.NotSame(t, orig.EndReason, cpy.EndReason)
	assert.Equal(t, reason, *cpy.EndReason)

	assert.NotSame(t, orig.StartAt, cpy.StartAt)
	assert.Equal(t, now, *cpy.StartAt)

	assert.NotSame(t, orig.EndAt, cpy.EndAt)
	assert.Equal(t, end, *cpy.EndAt)

	assert.NotSame(t, orig.RetryOfRunID, cpy.RetryOfRunID)
	assert.Equal(t, retryID, *cpy.RetryOfRunID)
}
