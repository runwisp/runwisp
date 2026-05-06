package protocol

type AuthResultMessage struct {
	Type         string `json:"type" binding:"required"`
	V            int    `json:"v,omitempty"`
	SentAt       string `json:"sentAt,omitempty"`
	Success      bool   `json:"success" binding:"required"`
	ConnectionID string `json:"connectionId,omitempty"`
	Error        string `json:"error,omitempty"`
}
