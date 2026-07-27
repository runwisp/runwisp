// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

import (
	"encoding/json"
)

type ServiceState uint

const (
	ServiceStateRunning ServiceState = iota
	ServiceStateDegraded
	ServiceStateStopped
	ServiceStateFatal
)

// Value returns the value of the enum.
func (op ServiceState) Value() any {
	if op >= ServiceState(len(ServiceStateValues)) {
		return nil
	}
	return ServiceStateValues[op]
}

var ServiceStateValues = []any{"running", "degraded", "stopped", "fatal"}
var ValuesToServiceState = map[any]ServiceState{
	ServiceStateValues[ServiceStateRunning]:  ServiceStateRunning,
	ServiceStateValues[ServiceStateDegraded]: ServiceStateDegraded,
	ServiceStateValues[ServiceStateStopped]:  ServiceStateStopped,
	ServiceStateValues[ServiceStateFatal]:    ServiceStateFatal,
}

func (op *ServiceState) UnmarshalJSON(raw []byte) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	*op = ValuesToServiceState[v]
	return nil
}

func (op ServiceState) MarshalJSON() ([]byte, error) {
	return json.Marshal(op.Value())
}
