// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package model

import "slices"

// HookAction is one verb a hook token may perform. Each maps 1:1 onto the
// session route of the same name: POST /api/hooks/tasks/{name}/<action>
// mirrors POST /api/tasks/{name}/<action>.
type HookAction string

const (
	HookRun     HookAction = "run"
	HookStart   HookAction = "start"
	HookStop    HookAction = "stop"
	HookRestart HookAction = "restart"
)

// HookActionsFor lists the actions a unit of this kind supports. A service
// has no `run` (it never makes a one-off run); everything else is shared.
func HookActionsFor(kind TaskKind) []HookAction {
	if kind.IsService() {
		return []HookAction{HookStart, HookStop, HookRestart}
	}
	return []HookAction{HookRun, HookStart, HookStop, HookRestart}
}

// HookToken is one [tasks.*]/[services.*] hook_tokens entry: a bearer secret
// plus the actions it grants. A nil Allow (the short string form) grants every
// action the unit supports.
type HookToken struct {
	Token string
	Allow []HookAction
}

// Allows reports whether this token may perform action.
func (h HookToken) Allows(action HookAction) bool {
	return h.Allow == nil || slices.Contains(h.Allow, action)
}
