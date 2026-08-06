// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package notify

import (
	"path"
	"strings"
)

// Predicate is the routing decision unit. Pure functions only; no I/O.
type Predicate func(*Event) bool

// MatchAll is the always-true predicate.
func MatchAll() Predicate { return func(*Event) bool { return true } }

// MatchKind succeeds when the event's kind is in the allowed set.
func MatchKind(kinds ...Kind) Predicate {
	if len(kinds) == 0 {
		return MatchAll()
	}
	set := make(map[Kind]struct{}, len(kinds))
	for _, k := range kinds {
		set[k] = struct{}{}
	}
	return func(ev *Event) bool {
		_, ok := set[ev.Kind]
		return ok
	}
}

// severityRank orders severities for MatchSeverity. Hoisted to package scope
// so the closure doesn't allocate the map on every call.
var severityRank = map[Severity]int{SevInfo: 0, SevWarn: 1, SevError: 2}

// MatchSeverity succeeds when the event severity is at least the threshold.
// Order: info < warn < error.
func MatchSeverity(min Severity) Predicate {
	threshold, ok := severityRank[min]
	if !ok {
		return MatchAll()
	}
	return func(ev *Event) bool {
		got, ok := severityRank[ev.Severity]
		if !ok {
			return false
		}
		return got >= threshold
	}
}

// MatchTaskGlob succeeds when the event's task name matches any of the
// supplied shell-style globs. Globs use path.Match semantics.
func MatchTaskGlob(patterns ...string) Predicate {
	clean := make([]string, 0, len(patterns))
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		clean = append(clean, p)
	}
	if len(clean) == 0 {
		return MatchAll()
	}
	return func(ev *Event) bool {
		for _, p := range clean {
			ok, err := path.Match(p, ev.TaskName)
			if err == nil && ok {
				return true
			}
		}
		return false
	}
}

// And composes predicates conjunctively (all must hold).
func And(preds ...Predicate) Predicate {
	if len(preds) == 0 {
		return MatchAll()
	}
	return func(ev *Event) bool {
		for _, p := range preds {
			if !p(ev) {
				return false
			}
		}
		return true
	}
}
