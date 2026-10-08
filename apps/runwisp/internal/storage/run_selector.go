// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package storage

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage/sqlcdb"
)

// nullable maps the empty-string "no filter" convention to a nil any value
// so the SQL gate `arg IS NULL OR field = arg` can short-circuit. Non-empty
// values box into the interface unchanged.
func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}

// nullableTime / nullableInt map the unset pointer to a nil gate; a present
// value boxes through unchanged so the comparison predicate applies. Time
// bounds are normalized to UTC so the SQL gate's plain TEXT comparison
// (created_at >= ?) lines up with the UTC-normalized values written by
// converters.go's utcPtr — otherwise a filter bound built with a different
// offset than the stored row could compare incorrectly even when the two
// instants are correctly ordered.
func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC()
}

func nullableInt(n *int) any {
	if n == nil {
		return nil
	}
	return *n
}

// nullableBool turns a boolean toggle into a gate: false leaves the gate open
// (nil), true closes it with a non-nil sentinel so the predicate (which never
// reads the value) takes effect.
func nullableBool(b bool) any {
	if !b {
		return nil
	}
	return 1
}

// statusSet renders a comma-separated status list into the pipe-delimited
// haystack the SQL set-membership gate matches against (e.g. |failed|crashed|).
// Blank tokens and the reserved FailureStatusToken are dropped (the latter is
// decoded into the separate match_failure gate); an all-blank/empty input
// returns nil so the gate stays fully open. sawFailure reports whether the
// failure token was present.
func statusSet(csv string) (set any, sawFailure bool) {
	var tokens []string
	for _, tok := range strings.Split(csv, ",") {
		switch tok = strings.TrimSpace(tok); tok {
		case "":
			// skip blank
		case model.FailureStatusToken:
			sawFailure = true
		default:
			tokens = append(tokens, tok)
		}
	}
	if len(tokens) == 0 {
		return nil, sawFailure
	}
	return "|" + strings.Join(tokens, "|") + "|", sawFailure
}

// buildRunFilterArgs decomposes a RunFilter into the filter-gate parameters
// shared by every selector-driven sqlc query (CountRunsFilteredParams carries
// exactly that set). Each gate holds either nil (gate open, predicate skipped
// via SQL IS NULL) or a concrete value the gate compares against.
//
// StatusSet is the pipe-delimited status haystack (|a|b|) the SQL
// set-membership gate matches a run's phase OR end reason against, rendered
// here so the SQL itself never branches per status. MatchFailure is 0 or 1
// (never nil): the SQL OR-branch `match_failure = 1 AND is_failure = 1` widens
// the status gate to the run's failure classification, composing with an empty
// status set (Failed selected alone still filters). The search input is
// truncated and lowercased into SearchTerm, a literal substring the SQL
// matches case-insensitively, so `_` (valid in task names) matches itself.
func buildRunFilterArgs(f model.RunFilter) sqlcdb.CountRunsFilteredParams {
	statusSetArg, sawFailure := statusSet(f.Status)
	var matchFailure int64
	if f.IsFailure || sawFailure {
		matchFailure = 1
	}
	args := sqlcdb.CountRunsFilteredParams{
		StatusSet:         statusSetArg,
		TaskNameFilter:    nullable(f.TaskName),
		SearchFilter:      nullable(f.Search),
		CreatedAfter:      nullableTime(f.CreatedAfter),
		CreatedBefore:     nullableTime(f.CreatedBefore),
		TriggeredByFilter: nullable(f.TriggeredBy),
		ExitCodeMin:       nullableInt(f.ExitCodeMin),
		ExitCodeMax:       nullableInt(f.ExitCodeMax),
		RetriesOnly:       nullableBool(f.RetriesOnly),
		MatchFailure:      matchFailure,
	}
	if f.Search != "" {
		s := f.Search
		if len(s) > maxSearchQueryLength {
			cut := maxSearchQueryLength
			for cut > 0 && !utf8.RuneStart(s[cut]) {
				cut--
			}
			s = s[:cut]
		}
		args.SearchTerm = strings.ToLower(s)
	}
	return args
}

// exceptIDsForSlice prepends a never-matching sentinel to the caller's
// ExceptIDs so sqlc's slice expansion behaves as "no exclusions" when the
// list is empty. sqlc.slice on an empty []string renders `NOT IN (NULL)`,
// which evaluates to NULL for every row — equivalent to "exclude
// everything", the opposite of the intent. The empty-string sentinel never
// matches a real ULID (or task name, for ForgetTaskRegistrationsExcept), so
// the NOT IN then matches all rows; for non-empty ExceptIDs the sentinel is
// harmless.
func exceptIDsForSlice(ids []string) []string {
	out := make([]string, 0, len(ids)+1)
	out = append(out, "")
	out = append(out, ids...)
	return out
}
