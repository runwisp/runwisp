package protocol

type PongMessage struct {
	Type   string `json:"type" binding:"required"`
	V      int    `json:"v,omitempty"`
	SentAt string `json:"sentAt,omitempty"`
}
