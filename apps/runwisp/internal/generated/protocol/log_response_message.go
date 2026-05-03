
package protocol

type LogResponseMessage struct {
  Type string `json:"type" binding:"required"`
  V int `json:"v,omitempty"`
  SentAt string `json:"sentAt,omitempty"`
  ID string `json:"id" binding:"required"`
  ExecutionID string `json:"executionId" binding:"required"`
  Data string `json:"data" binding:"required"`
  Offset int64 `json:"offset" binding:"required"`
  Final bool `json:"final" binding:"required"`
}