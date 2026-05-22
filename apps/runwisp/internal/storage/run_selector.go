// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"encoding/json"
	"strings"

	"github.com/runwisp/runwisp/internal/model"
)

// runFilterGateArgs decomposes a RunFilter into the nine positional parameters
// expected by the static filterGatesSQL predicate block:
//
//	end_reason gate, end_reason value,
//	status     gate, status     value,
//	task_name  gate, task_name  value,
//	search     gate, search pattern, search pattern
//
// The status input is dispatched here (end_reason column vs status column) so
// the SQL itself never branches. The search input is truncated and stripped of
// LIKE wildcards before the pattern is built.
func runFilterGateArgs(f model.RunFilter) []any {
	var endReason, statusPhase string
	switch model.EndReason(f.Status) {
	case model.ReasonSuccess, model.ReasonFailed, model.ReasonStopped,
		model.ReasonTimeout, model.ReasonCrashed, model.ReasonSkipped,
		model.ReasonLogOverflow:
		endReason = f.Status
	default:
		statusPhase = f.Status
	}
	var pattern string
	if f.Search != "" {
		s := f.Search
		if len(s) > MaxSearchQueryLength {
			s = s[:MaxSearchQueryLength]
		}
		s = strings.ReplaceAll(s, "%", "")
		s = strings.ReplaceAll(s, "_", "")
		pattern = "%" + s + "%"
	}
	return []any{
		endReason, endReason,
		statusPhase, statusPhase,
		f.TaskName, f.TaskName,
		f.Search, pattern, pattern,
	}
}

// idsJSON marshals an ID list to a JSON-array string accepted by
// `json_each(?)` inside the static IN / NOT IN subqueries. nil or empty
// becomes "[]" so the resulting `IN ()` is the empty set (no matches) and
// `NOT IN ()` is universal — both are exactly the safety-net behaviors we
// want when the selector side is unconstrained.
func idsJSON(ids []string) string {
	if len(ids) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(ids)
	return string(b)
}
