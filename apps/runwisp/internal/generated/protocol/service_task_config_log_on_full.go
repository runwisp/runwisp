// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

import (
	"encoding/json"
)

type ServiceTaskConfigLogOnFull uint

const (
	ServiceTaskConfigLogOnFullDropNew ServiceTaskConfigLogOnFull = iota
	ServiceTaskConfigLogOnFullDropOld
	ServiceTaskConfigLogOnFullKillTask
)

// Value returns the value of the enum.
func (op ServiceTaskConfigLogOnFull) Value() any {
	if op >= ServiceTaskConfigLogOnFull(len(ServiceTaskConfigLogOnFullValues)) {
		return nil
	}
	return ServiceTaskConfigLogOnFullValues[op]
}

var ServiceTaskConfigLogOnFullValues = []any{"drop_new", "drop_old", "kill_task"}
var ValuesToServiceTaskConfigLogOnFull = map[any]ServiceTaskConfigLogOnFull{
	ServiceTaskConfigLogOnFullValues[ServiceTaskConfigLogOnFullDropNew]:  ServiceTaskConfigLogOnFullDropNew,
	ServiceTaskConfigLogOnFullValues[ServiceTaskConfigLogOnFullDropOld]:  ServiceTaskConfigLogOnFullDropOld,
	ServiceTaskConfigLogOnFullValues[ServiceTaskConfigLogOnFullKillTask]: ServiceTaskConfigLogOnFullKillTask,
}

func (op *ServiceTaskConfigLogOnFull) UnmarshalJSON(raw []byte) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	*op = ValuesToServiceTaskConfigLogOnFull[v]
	return nil
}

func (op ServiceTaskConfigLogOnFull) MarshalJSON() ([]byte, error) {
	return json.Marshal(op.Value())
}
