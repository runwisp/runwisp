// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package notify

// Rule binds a predicate to a list of action IDs to invoke when the predicate
// matches. ActionIDs are resolved against an ActionRegistry at routing time;
// unknown IDs are skipped (validation happens at config load).
type Rule struct {
	Match     Predicate
	ActionIDs []string
}

// Router applies a flat list of rules in declaration order. Each event may
// match multiple rules; resulting actions are deduplicated.
type Router struct {
	rules    []Rule
	registry *ActionRegistry
}

// NewRouter wires a rule list to a registry.
func NewRouter(rules []Rule, registry *ActionRegistry) *Router {
	return &Router{rules: rules, registry: registry}
}

// Route returns the (deduplicated) Actions whose rule predicates match ev.
// The synthetic KindNotifyDeliveryFailed event is always routed only to the
// in-app channel via the dispatcher's direct path; the Router treats it as
// any other event but the dispatcher bypasses it before calling Route. Cycle
// guard lives in the dispatcher, not here.
func (r *Router) Route(ev *Event) []Action {
	if r == nil || ev == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(r.rules))
	out := make([]Action, 0, len(r.rules))
	for _, rule := range r.rules {
		if rule.Match == nil || !rule.Match(ev) {
			continue
		}
		for _, id := range rule.ActionIDs {
			if _, dup := seen[id]; dup {
				continue
			}
			a := r.registry.Get(id)
			if a == nil {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, a)
		}
	}
	return out
}
