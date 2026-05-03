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

func NewLogChunkMessage(executionID, data string, offset int64, final bool) protocol.LogChunkMessage {
	v, s := newEnvelopeFields()
	return protocol.LogChunkMessage{
		Type:        "log:chunk",
		V:           v,
		SentAt:      s,
		ExecutionID: executionID,
		Data:        data,
		Offset:      offset,
		Final:       final,
	}
}

func NewLogResponseMessage(requestID, executionID, data string, offset int64, final bool) protocol.LogResponseMessage {
	v, s := newEnvelopeFields()
	return protocol.LogResponseMessage{
		Type:        "log:response",
		V:           v,
		SentAt:      s,
		ID:          requestID,
		ExecutionID: executionID,
		Data:        data,
		Offset:      offset,
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
	"log:request":        decodeAs[protocol.LogRequestMessage],
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
