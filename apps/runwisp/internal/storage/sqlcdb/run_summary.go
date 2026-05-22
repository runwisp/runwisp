// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package sqlcdb

import "time"

// RunSummary holds aggregate run statistics surfaced by the GET /api/runs
// summary endpoint. Hand-written rather than auto-generated because sqlc emits
// the nullable MAX() column as interface{} — the public surface wants a typed
// *time.Time so callers can render or omit it without a type assertion.
type RunSummary struct {
	Total       int64      `json:"total" doc:"Total number of runs"`
	Success     int64      `json:"success" doc:"Number of successful runs"`
	Failed      int64      `json:"failed" doc:"Number of failed runs"`
	LastFailure *time.Time `json:"last_failure,omitempty" doc:"Timestamp of most recent failure"`
}
