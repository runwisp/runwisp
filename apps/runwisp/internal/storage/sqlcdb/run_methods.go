// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package sqlcdb

import "time"

// Copy creates a deep copy of the Run to prevent data races.
func (r *Run) Copy() *Run {
	if r == nil {
		return nil
	}
	cpy := *r
	if r.ExternalExecutionID != nil {
		eid := *r.ExternalExecutionID
		cpy.ExternalExecutionID = &eid
	}
	if r.EndReason != nil {
		er := *r.EndReason
		cpy.EndReason = &er
	}
	if r.StartAt != nil {
		sa := *r.StartAt
		cpy.StartAt = &sa
	}
	if r.EndAt != nil {
		ea := *r.EndAt
		cpy.EndAt = &ea
	}
	if r.RetryOfRunID != nil {
		rid := *r.RetryOfRunID
		cpy.RetryOfRunID = &rid
	}
	if r.DeletedAt != nil {
		da := *r.DeletedAt
		cpy.DeletedAt = &da
	}
	return &cpy
}

// End transitions a run to the ended phase with the given reason.
func (r *Run) End(reason EndReason, exitCode int, endAt time.Time) {
	r.Status = PhaseEnded
	r.EndReason = &reason
	r.ExitCode = exitCode
	r.EndAt = &endAt
}

// IsRetryable reports whether a run ended with a reason that warrants
// re-running it. Skipped runs are excluded — the policy already decided
// the firing was redundant; another retry just races the original again.
func (r *Run) IsRetryable() bool {
	if r.Status != PhaseEnded || r.EndReason == nil {
		return false
	}
	switch *r.EndReason {
	case ReasonSuccess, ReasonSkipped:
		return false
	default:
		return true
	}
}

// DisplayStatus returns the end reason string when ended, otherwise the phase.
func (r *Run) DisplayStatus() string {
	if r.Status == PhaseEnded {
		if r.EndReason != nil {
			return string(*r.EndReason)
		}
		return string(ReasonStopped)
	}
	return string(r.Status)
}
