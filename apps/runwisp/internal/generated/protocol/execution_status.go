
package protocol
import (  
  "encoding/json"
)
type ExecutionStatus uint

const (
  ExecutionStatusRunning ExecutionStatus = iota
  ExecutionStatusOk
  ExecutionStatusErr
  ExecutionStatusStopped
  ExecutionStatusTimeout
)

// Value returns the value of the enum.
func (op ExecutionStatus) Value() any {
	if op >= ExecutionStatus(len(ExecutionStatusValues)) {
		return nil
	}
	return ExecutionStatusValues[op]
}

var ExecutionStatusValues = []any{"running","ok","err","stopped","timeout"}
var ValuesToExecutionStatus = map[any]ExecutionStatus{
  ExecutionStatusValues[ExecutionStatusRunning]: ExecutionStatusRunning,
  ExecutionStatusValues[ExecutionStatusOk]: ExecutionStatusOk,
  ExecutionStatusValues[ExecutionStatusErr]: ExecutionStatusErr,
  ExecutionStatusValues[ExecutionStatusStopped]: ExecutionStatusStopped,
  ExecutionStatusValues[ExecutionStatusTimeout]: ExecutionStatusTimeout,
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