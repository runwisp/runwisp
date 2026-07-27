// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package cloud

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/model"
)

// ErrUnsupportedMessageType is returned by DecodeInboundMessage when the
// envelope's type is not in inboundDecoders. It is deliberately distinct from a
// decode failure of a *known* type: an unsupported type is a forward-compat
// event (cloud sent a message this daemon version doesn't implement yet), not a
// protocol violation. The session survives and no error frame is sent — see
// sessionRunner.handleInboundPayload and the forward-compat contract at the top
// of packages/asyncapi/asyncapi.yaml.
var ErrUnsupportedMessageType = errors.New("unsupported message type")

const ProtocolVersion = 2

func newEnvelopeFields() (int, string) {
	return ProtocolVersion, time.Now().UTC().Format(time.RFC3339Nano)
}

// NewPingMessage builds a heartbeat frame. stats, when non-nil, piggybacks a
// live system snapshot so the control plane can surface the runner's
// CPU/memory/identity without a dedicated message (add-only field; old cloud
// ignores it).
func NewPingMessage(stats *protocol.SystemStats) protocol.PingMessage {
	v, s := newEnvelopeFields()
	return protocol.PingMessage{Type: "ping", V: v, SentAt: s, SystemStats: stats}
}

// makeSystemStatsProvider adapts the daemon's model-typed stats source into the
// wire-typed provider the heartbeat loop calls. Returns nil when no source is
// wired so the session sends plain pings.
func makeSystemStatsProvider(fn func() model.SystemStats) func() *protocol.SystemStats {
	if fn == nil {
		return nil
	}
	return func() *protocol.SystemStats {
		return toProtocolSystemStats(fn())
	}
}

// toProtocolSystemStats maps the daemon's internal stats snapshot onto the wire
// type. Identity-only fields the cloud already learns at auth (name) or that
// could leak a local path (workDir) are intentionally dropped.
func toProtocolSystemStats(s model.SystemStats) *protocol.SystemStats {
	return &protocol.SystemStats{
		CpuUsage: s.CPUUsage,
		MemUsage: s.MemUsage,
		MemTotal: int(s.MemTotal),
		MemUsed:  int(s.MemUsed),
		CpuCores: s.CPUCores,
		Uptime:   s.Uptime,
		Version:  s.Version,
		Host:     s.Host,
		Os:       s.OS,
		Arch:     s.Arch,
	}
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

// NewLogSearchChunkMessage builds the single reply to a log:searchRequest.
// exhausted=true means the whole on-disk log was scanned; otherwise nextLine
// carries the resume cursor.
func NewLogSearchChunkMessage(requestID, executionID string, hits []protocol.HitsItem, nextLine int64, exhausted bool) protocol.LogSearchChunkMessage {
	v, s := newEnvelopeFields()
	return protocol.LogSearchChunkMessage{
		Type:        "log:searchChunk",
		V:           v,
		SentAt:      s,
		ID:          requestID,
		ExecutionID: executionID,
		Hits:        hits,
		NextLine:    int(nextLine),
		Exhausted:   exhausted,
	}
}

func NewProtocolErrorMessage(code, message, requestID, executionID string) protocol.ProtocolErrorMessage {
	v, s := newEnvelopeFields()
	return protocol.ProtocolErrorMessage{
		Type:        "error",
		V:           v,
		SentAt:      s,
		Code:        code,
		Message:     message,
		RequestID:   requestID,
		ExecutionID: executionID,
	}
}

// NewExecutionAckMessage confirms receipt of an execution:dispatch. Sent as
// soon as the dispatch passes validation, before the run is triggered, so the
// control plane can stop re-dispatching.
func NewExecutionAckMessage(executionID string) protocol.ExecutionAckMessage {
	v, s := newEnvelopeFields()
	return protocol.ExecutionAckMessage{
		Type:        "execution:ack",
		V:           v,
		SentAt:      s,
		ExecutionID: executionID,
	}
}

// NewServiceStatusMessage builds a daemon→cloud snapshot of a supervised
// service: its rollup state, desired/running counts, and per-instance slots.
// Sent on every instance lifecycle change so the control plane surfaces the
// latest view without a dedicated poll (additive daemon→cloud message; old
// cloud ignores it).
func NewServiceStatusMessage(snapshot model.ServiceSnapshot) protocol.ServiceStatusMessage {
	v, s := newEnvelopeFields()
	state := serviceStateEnum(snapshot.State)
	instances := make([]protocol.InstancesItem, 0, len(snapshot.Instances))
	for _, inst := range snapshot.Instances {
		instState := serviceInstanceStateEnum(inst.State)
		item := protocol.InstancesItem{
			Index:        inst.Index,
			State:        &instState,
			Pid:          inst.Pid,
			StartedAt:    inst.StartedAt,
			RestartCount: inst.RestartCount,
		}
		if inst.LastExitCode != nil {
			item.LastExitCode = *inst.LastExitCode
		}
		instances = append(instances, item)
	}
	return protocol.ServiceStatusMessage{
		Type:             "service:status",
		V:                v,
		SentAt:           s,
		TaskID:           snapshot.TaskName,
		State:            &state,
		DesiredInstances: snapshot.DesiredInstances,
		RunningInstances: snapshot.RunningInstances,
		Instances:        instances,
	}
}

// serviceStateEnum maps the daemon's string rollup state onto the wire enum,
// defaulting to "running" if an unrecognized value ever slips through (the enum
// is closed; this keeps the message well-formed rather than zero-valued by luck).
func serviceStateEnum(state string) protocol.ServiceState {
	if e, ok := protocol.ValuesToServiceState[state]; ok {
		return e
	}
	return protocol.ServiceStateRunning
}

func serviceInstanceStateEnum(state string) protocol.ServiceInstanceState {
	if e, ok := protocol.ValuesToServiceInstanceState[state]; ok {
		return e
	}
	return protocol.ServiceInstanceStateRunning
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

func hitsItemStreamFromString(stream string) protocol.HitsItemStream {
	switch stream {
	case "stderr":
		return protocol.HitsItemStreamStderr
	case "system":
		return protocol.HitsItemStreamSystem
	default:
		return protocol.HitsItemStreamStdout
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
	"log:searchRequest":  decodeAs[protocol.LogSearchRequestMessage],
	"log:listen":         decodeAs[protocol.LogListenMessage],
	"log:stop":           decodeAs[protocol.LogStopMessage],
	"agent:restart":      decodeAs[protocol.AgentRestartMessage],
	"service:apply":      decodeAs[protocol.ServiceApplyMessage],
	"service:control":    decodeAs[protocol.ServiceControlMessage],
	"service:remove":     decodeAs[protocol.ServiceRemoveMessage],
	"error":              decodeAs[protocol.ProtocolErrorMessage],
}

// supportedInboundTypes returns the sorted set of cloud→daemon message types
// this daemon understands — exactly the keys of inboundDecoders. It is what the
// daemon advertises in the X-Runner-Protocol-Features auth header so the cloud
// can gate behavior-dependent features it must fall back on for older daemons.
// Deriving it from inboundDecoders (rather than a hand-kept list) guarantees the
// advertisement can never drift from what handleInboundPayload actually handles.
func supportedInboundTypes() []string {
	types := make([]string, 0, len(inboundDecoders))
	for messageType := range inboundDecoders {
		types = append(types, messageType)
	}
	sort.Strings(types)
	return types
}

func DecodeInboundMessage(payload []byte) (any, error) {
	messageType, err := decodeMessageType(payload)
	if err != nil {
		return nil, fmt.Errorf("decode envelope: %w", err)
	}
	decoder, ok := inboundDecoders[messageType]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedMessageType, messageType)
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
