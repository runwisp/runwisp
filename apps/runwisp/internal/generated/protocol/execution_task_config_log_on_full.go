// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

import (
	"encoding/json"
)

type ExecutionTaskConfigLogOnFull uint

const (
	ExecutionTaskConfigLogOnFullDropNew ExecutionTaskConfigLogOnFull = iota
	ExecutionTaskConfigLogOnFullDropOld
	ExecutionTaskConfigLogOnFullKillTask
)

// Value returns the value of the enum.
func (op ExecutionTaskConfigLogOnFull) Value() any {
	if op >= ExecutionTaskConfigLogOnFull(len(ExecutionTaskConfigLogOnFullValues)) {
		return nil
	}
	return ExecutionTaskConfigLogOnFullValues[op]
}

var ExecutionTaskConfigLogOnFullValues = []any{"drop_new", "drop_old", "kill_task"}
var ValuesToExecutionTaskConfigLogOnFull = map[any]ExecutionTaskConfigLogOnFull{
	ExecutionTaskConfigLogOnFullValues[ExecutionTaskConfigLogOnFullDropNew]:  ExecutionTaskConfigLogOnFullDropNew,
	ExecutionTaskConfigLogOnFullValues[ExecutionTaskConfigLogOnFullDropOld]:  ExecutionTaskConfigLogOnFullDropOld,
	ExecutionTaskConfigLogOnFullValues[ExecutionTaskConfigLogOnFullKillTask]: ExecutionTaskConfigLogOnFullKillTask,
}

func (op *ExecutionTaskConfigLogOnFull) UnmarshalJSON(raw []byte) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	*op = ValuesToExecutionTaskConfigLogOnFull[v]
	return nil
}

func (op ExecutionTaskConfigLogOnFull) MarshalJSON() ([]byte, error) {
	return json.Marshal(op.Value())
}
