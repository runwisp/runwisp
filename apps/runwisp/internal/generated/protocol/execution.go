package protocol

import (
	"encoding/json"
)

type Execution struct {
	ID           string            `json:"id" binding:"required"`
	TaskID       string            `json:"taskId" binding:"required"`
	TaskName     string            `json:"taskName" binding:"required"`
	ExecutionID  string            `json:"executionId" binding:"required"`
	Priority     int               `json:"priority" binding:"required"`
	TriggeredBy  *TriggerType      `json:"triggeredBy" binding:"required"`
	InputValues  map[string]string `json:"inputValues" binding:"required"`
	Script       json.RawMessage   `json:"script" binding:"required"`
	Timeout      int               `json:"timeout" binding:"required"`
	LogUploadURL string            `json:"logUploadUrl" binding:"required"`
	LogPath      string            `json:"logPath" binding:"required"`
}
