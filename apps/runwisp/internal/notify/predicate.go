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

// MatchFailure succeeds when the event is classified as a failure. It is the
// single predicate behind the built-in failure route (catch-all and per-task
// notify), so per-task `failures` promotions/demotions re-route without
// touching notify config.
func MatchFailure() Predicate { return func(ev *Event) bool { return ev.IsFailure } }

// MatchOutcomes succeeds when the event's Outcome() token is in the allowed
// set. The token vocabulary is the same one `failures` uses — bare end-reason
// names (`failed`, `timeout`, `log_overflow`, …) — plus the non-run event
// tokens (`service.fatal`, `log.disk_pressure`, `started`), so a `[[route]]`
// and a task's `failures` policy speak one language.
func MatchOutcomes(tokens ...string) Predicate {
	if len(tokens) == 0 {
		return MatchAll()
	}
	set := make(map[string]struct{}, len(tokens))
	for _, t := range tokens {
		set[t] = struct{}{}
	}
	return func(ev *Event) bool {
		_, ok := set[ev.Outcome()]
		return ok
	}
}

// MatchTaskGlob succeeds when the event's task name matches the shell-style
// glob (path.Match semantics). A blank pattern matches everything.
func MatchTaskGlob(pattern string) Predicate {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return MatchAll()
	}
	return func(ev *Event) bool {
		ok, err := path.Match(pattern, ev.TaskName)
		return err == nil && ok
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
