package protocol

type LogReplayChunkMessage struct {
	Type        string      `json:"type" binding:"required"`
	V           int         `json:"v,omitempty"`
	SentAt      string      `json:"sentAt,omitempty"`
	ID          string      `json:"id" binding:"required"`
	ExecutionID string      `json:"executionId" binding:"required"`
	Lines       []LinesItem `json:"lines" binding:"required"`
	Final       bool        `json:"final" binding:"required"`
}
