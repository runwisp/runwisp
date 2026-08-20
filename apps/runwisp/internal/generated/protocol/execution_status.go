// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Code generated from packages/asyncapi/asyncapi.yaml; DO NOT EDIT.

package protocol

import (
	"encoding/json"
)

type ExecutionStatus uint

const (
	ExecutionStatusRunning ExecutionStatus = iota
	ExecutionStatusSucceeded
	ExecutionStatusFailed
	ExecutionStatusStopped
	ExecutionStatusTimeout
	ExecutionStatusSkipped
)

// Value returns the value of the enum.
func (op ExecutionStatus) Value() any {
	if op >= ExecutionStatus(len(ExecutionStatusValues)) {
		return nil
	}
	return ExecutionStatusValues[op]
}

var ExecutionStatusValues = []any{"running", "succeeded", "failed", "stopped", "timeout", "skipped"}
var ValuesToExecutionStatus = map[any]ExecutionStatus{
	ExecutionStatusValues[ExecutionStatusRunning]:   ExecutionStatusRunning,
	ExecutionStatusValues[ExecutionStatusSucceeded]: ExecutionStatusSucceeded,
	ExecutionStatusValues[ExecutionStatusFailed]:    ExecutionStatusFailed,
	ExecutionStatusValues[ExecutionStatusStopped]:   ExecutionStatusStopped,
	ExecutionStatusValues[ExecutionStatusTimeout]:   ExecutionStatusTimeout,
	ExecutionStatusValues[ExecutionStatusSkipped]:   ExecutionStatusSkipped,
}

func (op *ExecutionStatus) UnmarshalJSON(raw []byte) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	*op = ValuesToExecutionStatus[v]
	return nil
}

func (op ExecutionStatus) MarshalJSON() ([]byte, error) {
	return json.Marshal(op.Value())
}
