package protocol

type LogLineMessage struct {
	Type        string  `json:"type" binding:"required"`
	V           int     `json:"v,omitempty"`
	SentAt      string  `json:"sentAt,omitempty"`
	ExecutionID string  `json:"executionId" binding:"required"`
	N           int64   `json:"n" binding:"required"`
	Ts          int64   `json:"ts,omitempty"`
	Stream      *Stream `json:"stream" binding:"required"`
	Text        string  `json:"text" binding:"required"`
	Continued   bool    `json:"continued,omitempty"`
}
