// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// RunPhase captures the high-level lifecycle phase of a run.
type RunPhase string

const (
	PhasePending RunPhase = "pending"
	PhaseRunning RunPhase = "running"
	PhaseEnded   RunPhase = "ended"
)

// IsTerminal reports whether the phase represents a completed run.
func (p RunPhase) IsTerminal() bool {
	return p == PhaseEnded
}

// EndReason describes why a run ended.
type EndReason string

const (
	ReasonSuccess EndReason = "success"
	ReasonFailed  EndReason = "failed"
	ReasonStopped EndReason = "stopped"
	ReasonTimeout EndReason = "timeout"
	ReasonCrashed EndReason = "crashed"
	// ReasonSkipped marks runs that the concurrency policy rejected before
	// they ever executed (PolicySkip). Distinct from ReasonFailed so chronic
	// overlap on a `* * * * *` health probe doesn't pose as a real failure
	// to retries, notifications, or dashboards. Exit code is conventionally
	// recorded as -1.
	ReasonSkipped EndReason = "skipped"
	// ReasonLogOverflow marks runs that log_on_full = "kill_task" cancelled
	// because output exceeded log_max_size. Treated as a failure for retry,
	// notification, and UI purposes — the run failed to stay inside its log
	// budget — but recorded as a distinct reason so operators can see the
	// specific cause without inspecting logs.
	ReasonLogOverflow EndReason = "log_overflow"
	// ReasonQueueFull marks runs the queue overflow policy rejected because
	// the per-task queue_max cap was already full when the firing arrived.
	// Distinct from ReasonSkipped (which only applies under PolicySkip) so
	// dashboards can call out queue saturation as its own signal.
	ReasonQueueFull EndReason = "queue_full"
	// ReasonDSTSkipped marks the suppressed half of a DST fall-back duplicate.
	// The first wall-clock-equal firing runs normally; the second is recorded
	// for visibility but never executes — so an operator can see the dedup
	// happened instead of guessing why the schedule "felt off".
	ReasonDSTSkipped EndReason = "dst_skipped"
	// ReasonDaemonStopped marks runs that were still in flight when the daemon
	// shut down and exceeded the [daemon] shutdown_timeout window before they
	// could clean up. Distinct from ReasonStopped (which is operator-initiated
	// per-run) and ReasonCrashed (which is reserved for unrecoverable kills).
	ReasonDaemonStopped EndReason = "daemon_stopped"
	// ReasonMissed marks a scheduled cron tick (or a run of consecutive ticks)
	// that never executed because the daemon was down during its window. It is
	// detected at restart from persisted history, not at the time of the miss —
	// a stopped process cannot observe its own absence. The row is a browsable
	// audit record of the gap, recorded independently of the catch_up re-run
	// policy (even catch_up = "skip" records it). Exit code is conventionally
	// -1, like other never-executed reasons.
	ReasonMissed EndReason = "missed"
	// ReasonStartFailed marks the final, FATAL instance run of a service that
	// fast-failed more than `start_retries` times in a row without ever
	// reaching `healthy_after` of uptime. The supervisor stops restarting it;
	// this run row is the durable record of the give-up. Treated as a failure
	// for retry/notify/cloud classification.
	ReasonStartFailed EndReason = "start_failed"
)

// AllEndReasons is the canonical, ordered list of end-reason values. The order
// is load-bearing: it determines the enum order in the generated OpenAPI
// schema and downstream TypeScript types (`packages/common`). Keep new values
// at the end unless intentionally reordering the API surface.
var AllEndReasons = []EndReason{
	ReasonSuccess,
	ReasonFailed,
	ReasonStopped,
	ReasonTimeout,
	ReasonCrashed,
	ReasonSkipped,
	ReasonLogOverflow,
	ReasonQueueFull,
	ReasonDSTSkipped,
	ReasonDaemonStopped,
	ReasonMissed,
	ReasonStartFailed,
}

const endReasonSchemaName = "EndReason"

// Schema implements huma.SchemaProvider so EndReason appears as a named enum
// (`components.schemas.EndReason`) in the generated OpenAPI document instead
// of an inline `enum` repeated at every usage site. Downstream codegen
// (openapi-typescript → packages/common) picks this up as a single shared
// type alias.
func (EndReason) Schema(r huma.Registry) *huma.Schema {
	if _, ok := r.Map()[endReasonSchemaName]; !ok {
		enum := make([]any, len(AllEndReasons))
		for i, v := range AllEndReasons {
			enum[i] = string(v)
		}
		r.Map()[endReasonSchemaName] = &huma.Schema{
			Type:        huma.TypeString,
			Description: "Why a run ended. Set when status=ended.",
			Enum:        enum,
		}
	}
	return &huma.Schema{Ref: "#/components/schemas/" + endReasonSchemaName}
}

// EndReasonPtr returns a pointer to the given EndReason value.
func EndReasonPtr(r EndReason) *EndReason { return &r }

// TriggeredBy identifies how a run started.
type TriggeredBy string

const (
	TriggeredByCron    TriggeredBy = "cron"
	TriggeredByAPI     TriggeredBy = "api"
	TriggeredByCloud   TriggeredBy = "cloud"
	TriggeredByService TriggeredBy = "service"
	TriggeredByStartup TriggeredBy = "startup"
)

// Run is the domain representation of an execution. The storage layer keeps a
// row-shaped twin (sqlcdb.Run with DeletedAt) and converts at the boundary so
// no consumer outside storage sees row-internal fields.
type Run struct {
	ID                  string      `json:"id"`
	ExternalExecutionID *string     `json:"external_execution_id,omitempty"`
	TaskName            string      `json:"task_name"`
	Status              RunPhase    `json:"status" enum:"pending,running,ended" doc:"Run lifecycle phase"`
	EndReason           *EndReason  `json:"end_reason,omitempty"`
	ExitCode            int         `json:"exit_code"`
	StartAt             *time.Time  `json:"start_at,omitempty"`
	EndAt               *time.Time  `json:"end_at,omitempty"`
	TriggeredBy         TriggeredBy `json:"triggered_by" enum:"cron,api,cloud,service,startup" doc:"How the run was triggered"`
	CreatedAt           time.Time   `json:"created_at"`
	RetryAttempt        int         `json:"retry_attempt"`
	RetryOfRunID        *string     `json:"retry_of_run_id,omitempty"`
	InstanceIndex       int         `json:"instance_index"`
	// Params holds the resolved per-execution parameter values used for this
	// run (identity key → value). Nil for tasks without declared parameters, so
	// it is omitted from JSON/storage and adds no behaviour for the common case.
	Params map[string]string `json:"params,omitempty"`
}

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
	if r.Params != nil {
		params := make(map[string]string, len(r.Params))
		for k, v := range r.Params {
			params[k] = v
		}
		cpy.Params = params
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
// Missed runs are excluded too — they never executed, so there is no failed
// attempt to retry; catch_up already governs whether the tick is re-fired.
func (r *Run) IsRetryable() bool {
	if r.Status != PhaseEnded || r.EndReason == nil {
		return false
	}
	switch *r.EndReason {
	case ReasonSuccess, ReasonSkipped, ReasonMissed:
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

// RunSummary holds aggregate run statistics surfaced by the GET /api/runs
// summary endpoint.
type RunSummary struct {
	Total       int64      `json:"total" doc:"Total number of runs"`
	Success     int64      `json:"success" doc:"Number of successful runs"`
	Failed      int64      `json:"failed" doc:"Number of failed runs"`
	Missed      int64      `json:"missed" doc:"Number of scheduled runs missed during downtime"`
	LastFailure *time.Time `json:"last_failure,omitempty" doc:"Timestamp of most recent failure"`
}
