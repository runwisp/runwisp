
package protocol

type LogStopMessage struct {
  Type string `json:"type" binding:"required"`
  V int `json:"v,omitempty"`
  SentAt string `json:"sentAt,omitempty"`
  ExecutionID string `json:"executionId" binding:"required"`
}