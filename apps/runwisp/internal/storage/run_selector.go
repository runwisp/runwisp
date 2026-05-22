// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"strings"

	"github.com/runwisp/runwisp/internal/model"
)

// runFilterArgs is the shared set of filter-gate parameters threaded through
// every selector-driven sqlc query. Each Foo/FooFilter pair acts as a gate:
// when the Foo value is "", the gate is open and the predicate is skipped;
// when it is non-empty, the column must equal it. Search additionally drives
// the LIKE pattern via SearchPattern, which is pre-rendered here.
type runFilterArgs struct {
	EndReasonFilter   string
	StatusPhaseFilter string
	TaskNameFilter    string
	SearchFilter      string
	SearchPattern     string
}

// buildRunFilterArgs decomposes a RunFilter into the values consumed by the
// filter-gate predicates. The Status input is dispatched here (end_reason
// column vs status column) so the SQL itself never branches. The search
// input is truncated and stripped of LIKE wildcards before the pattern is
// built.
func buildRunFilterArgs(f model.RunFilter) runFilterArgs {
	args := runFilterArgs{
		TaskNameFilter: f.TaskName,
		SearchFilter:   f.Search,
	}
	switch model.EndReason(f.Status) {
	case model.ReasonSuccess, model.ReasonFailed, model.ReasonStopped,
		model.ReasonTimeout, model.ReasonCrashed, model.ReasonSkipped,
		model.ReasonLogOverflow:
		args.EndReasonFilter = f.Status
	default:
		args.StatusPhaseFilter = f.Status
	}
	if f.Search != "" {
		s := f.Search
		if len(s) > MaxSearchQueryLength {
			s = s[:MaxSearchQueryLength]
		}
		s = strings.ReplaceAll(s, "%", "")
		s = strings.ReplaceAll(s, "_", "")
		args.SearchPattern = "%" + s + "%"
	}
	return args
}

// exceptIDsForSlice prepends a never-matching sentinel to the caller's
// ExceptIDs so sqlc's slice expansion behaves as "no exclusions" when the
// list is empty. sqlc.slice on an empty []string renders `NOT IN (NULL)`,
// which evaluates to NULL for every row — equivalent to "exclude
// everything", the opposite of the intent. The empty-string sentinel never
// matches a real ULID, so `NOT IN (”)` matches all rows; for non-empty
// ExceptIDs the sentinel is harmless.
func exceptIDsForSlice(ids []string) []string {
	out := make([]string, 0, len(ids)+1)
	out = append(out, "")
	out = append(out, ids...)
	return out
}
