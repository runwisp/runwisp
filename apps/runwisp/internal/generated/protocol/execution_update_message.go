package protocol

import (
	"time"
)

type ExecutionUpdateMessage struct {
	Type        string           `json:"type" binding:"required"`
	V           int              `json:"v,omitempty"`
	SentAt      string           `json:"sentAt,omitempty"`
	ExecutionID string           `json:"executionId" binding:"required"`
	Status      *ExecutionStatus `json:"status" binding:"required"`
	ExitCode    *int             `json:"exitCode,omitempty"`
	StartedAt   *time.Time       `json:"startedAt,omitempty"`
	FinishedAt  *time.Time       `json:"finishedAt,omitempty"`
}
