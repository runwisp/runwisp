// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"fmt"
	"time"
)

// RunFilter mirrors the read-side filter shape used by the runs list endpoint.
// Reused inside RunSelector so bulk operations target the same population the
// user sees in the UI. Every field is optional — the zero value selects every
// (non-deleted) run.
type RunFilter struct {
	// Status is a comma-separated set of statuses (phases and/or end reasons);
	// a run matches if its phase OR its end reason is in the set. A single
	// value (the common case) is just a one-element set.
	Status        string     `json:"status,omitempty"            doc:"Comma-separated run statuses (phase or end reason); a run matches any listed value"`
	TaskName      string     `json:"task_name,omitempty"         doc:"Filter by task name"`
	Search        string     `json:"search,omitempty"            doc:"Search query against task_name / id"`
	CreatedAfter  *time.Time `json:"created_after,omitempty"     doc:"Only runs created at or after this time"`
	CreatedBefore *time.Time `json:"created_before,omitempty"    doc:"Only runs created at or before this time"`
	TriggeredBy   string     `json:"triggered_by,omitempty"   doc:"Filter by what triggered the run (cron/api/cloud/service/startup)"`
	// ExitCodeMin / ExitCodeMax bound the exit code to an inclusive integer
	// range; either end may be set alone (open on the other side). The UI's
	// friendly expression (e.g. ">100 <150") is normalized to these bounds
	// before the wire, so the daemon only ever sees a plain range.
	ExitCodeMin *int `json:"exit_code_min,omitempty" doc:"Only runs whose exit code is >= this (inclusive)"`
	ExitCodeMax *int `json:"exit_code_max,omitempty" doc:"Only runs whose exit code is <= this (inclusive)"`
	RetriesOnly bool `json:"retries_only,omitempty"  doc:"Only runs that are a retry (retry_attempt > 0)"`
}

// RunSelector is the contract between UI and server for every bulk operation.
// It has two modes:
//
//	MatchAll = true:  every run matching Filter, minus ExceptIDs.
//	MatchAll = false: only the runs in IDs.
//
// One mode is required; mixing them is a validation error.
type RunSelector struct {
	MatchAll  bool      `json:"match_all,omitempty"  doc:"When true, selects every run matching Filter except those listed in ExceptIDs"`
	Filter    RunFilter `json:"filter,omitempty"     doc:"Filter to apply when MatchAll is true"`
	IDs       []string  `json:"ids,omitempty"        doc:"Explicit run IDs to select when MatchAll is false"`
	ExceptIDs []string  `json:"except_ids,omitempty" doc:"IDs to exclude when MatchAll is true"`
}

// Validate enforces the "exactly one mode" rule.
func (s RunSelector) Validate() error {
	if s.MatchAll {
		if len(s.IDs) > 0 {
			return fmt.Errorf("ids must be empty when match_all is true")
		}
		return nil
	}
	if len(s.IDs) == 0 {
		return fmt.Errorf("ids must be non-empty when match_all is false")
	}
	if len(s.ExceptIDs) > 0 {
		return fmt.Errorf("except_ids must be empty when match_all is false")
	}
	return nil
}
