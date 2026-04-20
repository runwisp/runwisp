package protocol

type PingMessage struct {
	Type   string `json:"type" binding:"required"`
	V      int    `json:"v,omitempty"`
	SentAt string `json:"sentAt,omitempty"`
	ID     string `json:"id,omitempty"`
}
