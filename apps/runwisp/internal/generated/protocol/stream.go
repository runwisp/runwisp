package protocol

import (
	"encoding/json"
)

type Stream uint

const (
	StreamStdout Stream = iota
	StreamStderr
	StreamSystem
)

// Value returns the value of the enum.
func (op Stream) Value() any {
	if op >= Stream(len(StreamValues)) {
		return nil
	}
	return StreamValues[op]
}

var StreamValues = []any{"stdout", "stderr", "system"}
var ValuesToStream = map[any]Stream{
	StreamValues[StreamStdout]: StreamStdout,
	StreamValues[StreamStderr]: StreamStderr,
	StreamValues[StreamSystem]: StreamSystem,
}

func (op *Stream) UnmarshalJSON(raw []byte) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	*op = ValuesToStream[v]
	return nil
}

func (op Stream) MarshalJSON() ([]byte, error) {
	return json.Marshal(op.Value())
}
