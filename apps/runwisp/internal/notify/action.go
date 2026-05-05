// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package notify

import "context"

// Action is the unit the dispatcher pumps events through. The interface is the
// future-extension seam: a Slack channel and a "trigger task X on event Y"
// action both implement it. Adding a new action type does not require any
// changes to dispatcher, router, or service plumbing.
type Action interface {
	// ID is a stable, log-friendly identifier (e.g. "slack:slack-ops").
	ID() string

	// Match is a cheap predicate that lets an action filter events further than
	// the router already did. False here causes the dispatcher to skip the
	// action without enqueuing.
	Match(*Event) bool

	// Execute performs the side effect. Implementations must respect ctx
	// (cancel, deadline) and return promptly when ctx is done.
	Execute(ctx context.Context, ev *Event) error
}

// ActionRegistry maps action IDs to their Action implementations. Built once
// at Service.Start from the resolved configuration (only configured provider
// types are constructed — no Slack goroutine if no Slack notifier is
// declared).
type ActionRegistry struct {
	actions map[string]Action
}

// NewActionRegistry constructs a registry from the supplied actions. Duplicate
// IDs cause the latter entry to win — callers should validate uniqueness
// upstream during config load.
func NewActionRegistry(actions []Action) *ActionRegistry {
	r := &ActionRegistry{actions: make(map[string]Action, len(actions))}
	for _, a := range actions {
		r.actions[a.ID()] = a
	}
	return r
}

// Get returns the action for id, or nil if not present.
func (r *ActionRegistry) Get(id string) Action {
	return r.actions[id]
}

// IDs returns a snapshot of all registered action IDs (unordered).
func (r *ActionRegistry) IDs() []string {
	ids := make([]string, 0, len(r.actions))
	for id := range r.actions {
		ids = append(ids, id)
	}
	return ids
}

// Each iterates all (id, action) pairs. Order is unspecified.
func (r *ActionRegistry) Each(fn func(id string, a Action)) {
	for id, a := range r.actions {
		fn(id, a)
	}
}
