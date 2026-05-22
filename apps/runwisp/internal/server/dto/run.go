// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package dto defines the wire-shaped types the server returns to REST and
// SSE clients. Keeping these distinct from the storage rows (sqlcdb.Run et al.)
// lets the daemon evolve its on-disk schema without rewriting the HTTP API,
// and lets the API hide row-internal fields (deleted_at, log_path) from
// clients without leaking them through accidental JSON serialization.
package dto

import (
	"time"

	"github.com/runwisp/runwisp/internal/storage/sqlcdb"
)

// Run is the API-facing projection of a run row. Field shape and JSON keys
// are stable wire contract — changes here ripple into openapi.json and the
// generated TypeScript types in packages/common.
type Run struct {
	ID                  string             `json:"id"`
	ExternalExecutionID *string            `json:"external_execution_id,omitempty"`
	TaskName            string             `json:"task_name"`
	Status              sqlcdb.RunPhase    `json:"status" enum:"pending,running,ended" doc:"Run lifecycle phase"`
	EndReason           *sqlcdb.EndReason  `json:"end_reason,omitempty"`
	ExitCode            int                `json:"exit_code"`
	StartAt             *time.Time         `json:"start_at,omitempty"`
	EndAt               *time.Time         `json:"end_at,omitempty"`
	TriggeredBy         sqlcdb.TriggeredBy `json:"triggered_by" enum:"cron,api,cloud,service" doc:"How the run was triggered"`
	CreatedAt           time.Time          `json:"created_at"`
	RetryAttempt        int                `json:"retry_attempt"`
	RetryOfRunID        *string            `json:"retry_of_run_id,omitempty"`
	InstanceIndex       int                `json:"instance_index"`
}

// FromSqlcdb projects a storage row onto the wire shape, dropping
// row-internal fields (DeletedAt) the API never returns. Pointer fields are
// shared with the source — callers must not mutate the source row after
// conversion if a parallel reader could observe the same DTO.
func FromSqlcdb(r *sqlcdb.Run) *Run {
	if r == nil {
		return nil
	}
	return &Run{
		ID:                  r.ID,
		ExternalExecutionID: r.ExternalExecutionID,
		TaskName:            r.TaskName,
		Status:              r.Status,
		EndReason:           r.EndReason,
		ExitCode:            r.ExitCode,
		StartAt:             r.StartAt,
		EndAt:               r.EndAt,
		TriggeredBy:         r.TriggeredBy,
		CreatedAt:           r.CreatedAt,
		RetryAttempt:        r.RetryAttempt,
		RetryOfRunID:        r.RetryOfRunID,
		InstanceIndex:       r.InstanceIndex,
	}
}

// IsRetryable reports whether a run ended with a reason that warrants
// re-running it. Mirrors the sqlcdb.Run method so TUI/apiclient consumers
// (which see dto.Run, not the storage row) can make the same retry decisions
// the daemon does.
func (r *Run) IsRetryable() bool {
	if r.Status != sqlcdb.PhaseEnded || r.EndReason == nil {
		return false
	}
	switch *r.EndReason {
	case sqlcdb.ReasonSuccess, sqlcdb.ReasonSkipped:
		return false
	default:
		return true
	}
}

// DisplayStatus returns the end reason string when ended, otherwise the phase.
// Used by TUI/UI rendering paths that read dto.Run off the wire.
func (r *Run) DisplayStatus() string {
	if r.Status == sqlcdb.PhaseEnded {
		if r.EndReason != nil {
			return string(*r.EndReason)
		}
		return string(sqlcdb.ReasonStopped)
	}
	return string(r.Status)
}

// FromSqlcdbSlice converts a slice of storage rows into a slice of wire DTOs.
// Returns a non-nil empty slice for an empty input so huma's response shape
// stays consistent (`"runs": []` rather than `"runs": null`).
func FromSqlcdbSlice(rows []sqlcdb.Run) []Run {
	out := make([]Run, len(rows))
	for i := range rows {
		out[i] = *FromSqlcdb(&rows[i])
	}
	return out
}
