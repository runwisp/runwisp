package protocol

type LogReplayRequestMessage struct {
	Type        string `json:"type" binding:"required"`
	V           int    `json:"v,omitempty"`
	SentAt      string `json:"sentAt,omitempty"`
	ID          string `json:"id" binding:"required"`
	ExecutionID string `json:"executionId" binding:"required"`
	FromLine    int64  `json:"fromLine" binding:"required"`
	Limit       int64  `json:"limit" binding:"required"`
}
