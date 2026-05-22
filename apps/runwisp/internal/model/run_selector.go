// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package model

import "fmt"

// RunFilter mirrors the read-side filter shape used by the runs list endpoint.
// Reused inside RunSelector so bulk operations target the same population the
// user sees in the UI.
type RunFilter struct {
	Status   string `json:"status,omitempty"    doc:"Filter by run status (phase or end reason)"`
	TaskName string `json:"task_name,omitempty" doc:"Filter by task name"`
	Search   string `json:"search,omitempty"    doc:"Search query against task_name / id"`
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
