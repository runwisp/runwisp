package protocol

import (
	"encoding/json"
)

type TriggerType uint

const (
	TriggerTypeManual TriggerType = iota
	TriggerTypeSchedule
	TriggerTypeSuccess
	TriggerTypeFailure
)

// Value returns the value of the enum.
func (op TriggerType) Value() any {
	if op >= TriggerType(len(TriggerTypeValues)) {
		return nil
	}
	return TriggerTypeValues[op]
}

var TriggerTypeValues = []any{"manual", "schedule", "success", "failure"}
var ValuesToTriggerType = map[any]TriggerType{
	TriggerTypeValues[TriggerTypeManual]:   TriggerTypeManual,
	TriggerTypeValues[TriggerTypeSchedule]: TriggerTypeSchedule,
	TriggerTypeValues[TriggerTypeSuccess]:  TriggerTypeSuccess,
	TriggerTypeValues[TriggerTypeFailure]:  TriggerTypeFailure,
}

func (op *TriggerType) UnmarshalJSON(raw []byte) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	*op = ValuesToTriggerType[v]
	return nil
}

func (op TriggerType) MarshalJSON() ([]byte, error) {
	return json.Marshal(op.Value())
}
