// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

import (
	"encoding/json"
)

type Action uint

const (
	ActionStart Action = iota
	ActionStop
	ActionRestart
)

// Value returns the value of the enum.
func (op Action) Value() any {
	if op >= Action(len(ActionValues)) {
		return nil
	}
	return ActionValues[op]
}

var ActionValues = []any{"start", "stop", "restart"}
var ValuesToAction = map[any]Action{
	ActionValues[ActionStart]:   ActionStart,
	ActionValues[ActionStop]:    ActionStop,
	ActionValues[ActionRestart]: ActionRestart,
}

func (op *Action) UnmarshalJSON(raw []byte) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	*op = ValuesToAction[v]
	return nil
}

func (op Action) MarshalJSON() ([]byte, error) {
	return json.Marshal(op.Value())
}
