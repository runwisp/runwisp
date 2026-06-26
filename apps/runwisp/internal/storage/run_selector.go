// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package storage

import (
	"strings"
	"time"

	"github.com/runwisp/runwisp/internal/model"
)

// runFilterArgs is the shared set of filter-gate parameters threaded through
// every selector-driven sqlc query. Each filter field is an interface{} so
// it can hold either nil (gate open, predicate skipped via SQL IS NULL) or
// a concrete value the gate compares against. Search additionally drives the
// LIKE pattern via SearchPattern, which is pre-rendered here.
type runFilterArgs struct {
	// StatusSet is the pipe-delimited status haystack (|a|b|) the SQL
	// set-membership gate matches a run's phase OR end reason against.
	StatusSet         interface{}
	TaskNameFilter    interface{}
	SearchFilter      interface{}
	SearchPattern     string
	CreatedAfter      interface{}
	CreatedBefore     interface{}
	TriggeredByFilter interface{}
	ExitCodeMin       interface{}
	ExitCodeMax       interface{}
	RetriesOnly       interface{}
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

// nullableTime / nullableInt map the unset pointer to a nil gate; a present
// value boxes through unchanged so the comparison predicate applies.
func nullableTime(t *time.Time) interface{} {
	if t == nil {
		return nil
	}
	return *t
}

func nullableInt(n *int) interface{} {
	if n == nil {
		return nil
	}
	return *n
}

// nullableBool turns a boolean toggle into a gate: false leaves the gate open
// (nil), true closes it with a non-nil sentinel so the predicate (which never
// reads the value) takes effect.
func nullableBool(b bool) interface{} {
	if !b {
		return nil
	}
	return 1
}

// statusSet renders a comma-separated status list into the pipe-delimited
// haystack the SQL set-membership gate matches against (e.g. |failed|crashed|).
// Blank tokens are dropped; an all-blank/empty input returns nil so the gate
// stays fully open. A single status is just a one-element set.
func statusSet(csv string) interface{} {
	var b strings.Builder
	for _, tok := range strings.Split(csv, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if b.Len() == 0 {
			b.WriteByte('|')
		}
		b.WriteString(tok)
		b.WriteByte('|')
	}
	if b.Len() == 0 {
		return nil
	}
	return b.String()
}

// buildRunFilterArgs decomposes a RunFilter into the values consumed by the
// filter-gate predicates. The status set is rendered here so the SQL itself
// never branches per status. The search input is truncated and stripped of
// LIKE wildcards before the pattern is built.
func buildRunFilterArgs(f model.RunFilter) runFilterArgs {
	args := runFilterArgs{
		StatusSet:         statusSet(f.Status),
		TaskNameFilter:    nullable(f.TaskName),
		SearchFilter:      nullable(f.Search),
		CreatedAfter:      nullableTime(f.CreatedAfter),
		CreatedBefore:     nullableTime(f.CreatedBefore),
		TriggeredByFilter: nullable(f.TriggeredBy),
		ExitCodeMin:       nullableInt(f.ExitCodeMin),
		ExitCodeMax:       nullableInt(f.ExitCodeMax),
		RetriesOnly:       nullableBool(f.RetriesOnly),
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
