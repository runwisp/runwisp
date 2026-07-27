// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

import (
	"encoding/json"
)

type ServiceRestartBackoff uint

const (
	ServiceRestartBackoffConstant ServiceRestartBackoff = iota
	ServiceRestartBackoffLinear
	ServiceRestartBackoffExponential
)

// Value returns the value of the enum.
func (op ServiceRestartBackoff) Value() any {
	if op >= ServiceRestartBackoff(len(ServiceRestartBackoffValues)) {
		return nil
	}
	return ServiceRestartBackoffValues[op]
}

var ServiceRestartBackoffValues = []any{"constant", "linear", "exponential"}
var ValuesToServiceRestartBackoff = map[any]ServiceRestartBackoff{
	ServiceRestartBackoffValues[ServiceRestartBackoffConstant]:    ServiceRestartBackoffConstant,
	ServiceRestartBackoffValues[ServiceRestartBackoffLinear]:      ServiceRestartBackoffLinear,
	ServiceRestartBackoffValues[ServiceRestartBackoffExponential]: ServiceRestartBackoffExponential,
}

func (op *ServiceRestartBackoff) UnmarshalJSON(raw []byte) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	*op = ValuesToServiceRestartBackoff[v]
	return nil
}

func (op ServiceRestartBackoff) MarshalJSON() ([]byte, error) {
	return json.Marshal(op.Value())
}
