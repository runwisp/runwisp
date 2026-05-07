// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/runwisp/runwisp/internal/generated/protocol"
)

const ProtocolVersion = 2

func newEnvelopeFields() (int, string) {
	return ProtocolVersion, time.Now().UTC().Format(time.RFC3339Nano)
}

func NewPingMessage() protocol.PingMessage {
	v, s := newEnvelopeFields()
	return protocol.PingMessage{Type: "ping", V: v, SentAt: s}
}

func NewExecutionUpdateMessage(executionID string, status protocol.ExecutionStatus, exitCode *int, startedAt, finishedAt *time.Time) protocol.ExecutionUpdateMessage {
	v, s := newEnvelopeFields()
	return protocol.ExecutionUpdateMessage{
		Type:        "execution:update",
		V:           v,
		SentAt:      s,
		ExecutionID: executionID,
		Status:      &status,
		ExitCode:    exitCode,
		StartedAt:   startedAt,
		FinishedAt:  finishedAt,
	}
}

// NewLogLineMessage builds a single line-event push.
func NewLogLineMessage(executionID string, n, ts int64, stream, text string, continued bool) protocol.LogLineMessage {
	v, s := newEnvelopeFields()
	streamEnum := streamEnumFromString(stream)
	return protocol.LogLineMessage{
		Type:        "log:line",
		V:           v,
		SentAt:      s,
		ExecutionID: executionID,
		N:           n,
		Ts:          ts,
		Stream:      &streamEnum,
		Text:        text,
		Continued:   continued,
	}
}

// NewLogReplayChunkMessage builds one page of historical lines for a
// log:replayRequest reply. final=true marks the last chunk in the reply set.
func NewLogReplayChunkMessage(requestID, executionID string, lines []protocol.LinesItem, final bool) protocol.LogReplayChunkMessage {
	v, s := newEnvelopeFields()
	return protocol.LogReplayChunkMessage{
		Type:        "log:replayChunk",
		V:           v,
		SentAt:      s,
		ID:          requestID,
		ExecutionID: executionID,
		Lines:       lines,
		Final:       final,
	}
}

func NewProtocolErrorMessage(code, message, requestID string) protocol.ProtocolErrorMessage {
	v, s := newEnvelopeFields()
	return protocol.ProtocolErrorMessage{
		Type:      "error",
		V:         v,
		SentAt:    s,
		Code:      code,
		Message:   message,
		RequestID: requestID,
	}
}

func streamEnumFromString(stream string) protocol.Stream {
	switch stream {
	case "stderr":
		return protocol.StreamStderr
	case "system":
		return protocol.StreamSystem
	default:
		return protocol.StreamStdout
	}
}

func linesItemStreamFromString(stream string) protocol.LinesItemStream {
	switch stream {
	case "stderr":
		return protocol.LinesItemStreamStderr
	case "system":
		return protocol.LinesItemStreamSystem
	default:
		return protocol.LinesItemStreamStdout
	}
}

func decodeAs[T any](payload []byte) (any, error) {
	var msg T
	if err := decodeStrict(payload, &msg); err != nil {
		return nil, err
	}
	return msg, nil
}

var inboundDecoders = map[string]func([]byte) (any, error){
	"auth:result":        decodeAs[protocol.AuthResultMessage],
	"execution:dispatch": decodeAs[protocol.ExecutionDispatchMessage],
	"execution:stop":     decodeAs[protocol.ExecutionStopMessage],
	"pong":               decodeAs[protocol.PongMessage],
	"log:replayRequest":  decodeAs[protocol.LogReplayRequestMessage],
	"log:listen":         decodeAs[protocol.LogListenMessage],
	"log:stop":           decodeAs[protocol.LogStopMessage],
	"error":              decodeAs[protocol.ProtocolErrorMessage],
}

func DecodeInboundMessage(payload []byte) (any, error) {
	messageType, err := decodeMessageType(payload)
	if err != nil {
		return nil, fmt.Errorf("decode envelope: %w", err)
	}
	decoder, ok := inboundDecoders[messageType]
	if !ok {
		return nil, fmt.Errorf("unsupported message type: %s", messageType)
	}
	return decoder(payload)
}

func EncodeOutboundMessage(message any) ([]byte, error) {
	return json.Marshal(message)
}

func decodeStrict(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))

	if err := decoder.Decode(target); err != nil {
		return err
	}

	if decoder.More() {
		return fmt.Errorf("unexpected trailing JSON payload")
	}

	return nil
}

func decodeMessageType(payload []byte) (string, error) {
	var envelope struct {
		Type string `json:"type"`
	}

	if err := json.Unmarshal(payload, &envelope); err != nil {
		return "", err
	}

	return envelope.Type, nil
}
