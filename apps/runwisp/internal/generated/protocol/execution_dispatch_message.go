
package protocol

type ExecutionDispatchMessage struct {
  Type string `json:"type" binding:"required"`
  V int `json:"v,omitempty"`
  SentAt string `json:"sentAt,omitempty"`
  Execution *Execution `json:"execution" binding:"required"`
}