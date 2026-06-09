// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"strings"

	"github.com/runwisp/runwisp/internal/model"
)

// runFilterArgs is the shared set of filter-gate parameters threaded through
// every selector-driven sqlc query. Each filter field is an interface{} so
// it can hold either nil (gate open, predicate skipped via SQL IS NULL) or
// a non-empty string (column must equal it). Search additionally drives the
// LIKE pattern via SearchPattern, which is pre-rendered here.
type runFilterArgs struct {
	EndReasonFilter   interface{}
	StatusPhaseFilter interface{}
	TaskNameFilter    interface{}
	SearchFilter      interface{}
	SearchPattern     string
}

// nullable maps the empty-string "no filter" convention to a nil interface{}
// so the SQL gate `arg IS NULL OR field = arg` can short-circuit. Non-empty
// values box into the interface unchanged.
func nullable(v string) interface{} {
	if v == "" {
		return nil
	}
	return v
}

// buildRunFilterArgs decomposes a RunFilter into the values consumed by the
// filter-gate predicates. The Status input is dispatched here (end_reason
// column vs status column) so the SQL itself never branches. The search
// input is truncated and stripped of LIKE wildcards before the pattern is
// built.
func buildRunFilterArgs(f model.RunFilter) runFilterArgs {
	args := runFilterArgs{
		TaskNameFilter: nullable(f.TaskName),
		SearchFilter:   nullable(f.Search),
	}
	switch model.EndReason(f.Status) {
	case model.ReasonSuccess, model.ReasonFailed, model.ReasonStopped,
		model.ReasonTimeout, model.ReasonCrashed, model.ReasonSkipped,
		model.ReasonLogOverflow:
		args.EndReasonFilter = nullable(f.Status)
	default:
		args.StatusPhaseFilter = nullable(f.Status)
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
