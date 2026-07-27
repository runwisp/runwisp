// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

import (
	"encoding/json"
)

type ServiceInstanceState uint

const (
	ServiceInstanceStateRunning ServiceInstanceState = iota
	ServiceInstanceStateRestarting
	ServiceInstanceStateStopped
	ServiceInstanceStateFatal
)

// Value returns the value of the enum.
func (op ServiceInstanceState) Value() any {
	if op >= ServiceInstanceState(len(ServiceInstanceStateValues)) {
		return nil
	}
	return ServiceInstanceStateValues[op]
}

var ServiceInstanceStateValues = []any{"running", "restarting", "stopped", "fatal"}
var ValuesToServiceInstanceState = map[any]ServiceInstanceState{
	ServiceInstanceStateValues[ServiceInstanceStateRunning]:    ServiceInstanceStateRunning,
	ServiceInstanceStateValues[ServiceInstanceStateRestarting]: ServiceInstanceStateRestarting,
	ServiceInstanceStateValues[ServiceInstanceStateStopped]:    ServiceInstanceStateStopped,
	ServiceInstanceStateValues[ServiceInstanceStateFatal]:      ServiceInstanceStateFatal,
}

func (op *ServiceInstanceState) UnmarshalJSON(raw []byte) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	*op = ValuesToServiceInstanceState[v]
	return nil
}

func (op ServiceInstanceState) MarshalJSON() ([]byte, error) {
	return json.Marshal(op.Value())
}
