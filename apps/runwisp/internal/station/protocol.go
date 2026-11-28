// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
)

// Subprotocol is the WebSocket subprotocol this daemon speaks; it is the
// protocol's only version number.
const Subprotocol = "runwisp.v1"

// Message types this daemon sends or acts on.
const (
	typeHello            = "hello"
	typeWelcome          = "welcome"
	typeGoodbye          = "goodbye"
	typePing             = "ping"
	typePong             = "pong"
	typeResult           = "result"
	typeAck              = "ack"
	typeAgentUpdate      = "agent:update"
	typeAgentStats       = "agent:stats"
	typeAgentRestart     = "agent:restart"
	typeAgentUpgrade     = "agent:upgrade"
	typeAgentCredentials = "agent:credentials"
	typeTasksSync        = "tasks:sync"
	typeExecDispatch     = "execution:dispatch"
	typeExecStop         = "execution:stop"
	typeExecUpdate       = "execution:update"
	typeCancel           = "cancel"
	typeLogSubscribe     = "log:subscribe"
	typeLogLines         = "log:lines"
	typeLogSearch        = "log:search"
	typeLogSearchHits    = "log:search:hits"
	typeUploadRequest    = "upload:request"
	typeServiceApply     = "service:apply"
	typeServiceControl   = "service:control"
	typeServiceRemove    = "service:remove"
	typeServiceStatus    = "service:status"
)

// encodeFrame builds one frame from env's header fields and data (nil for
// none). The payload is spliced in after the header rather than marshalled
// as the envelope's json.RawMessage, which json re-scans and copies: that
// costs more than encoding a log batch itself.
func encodeFrame(env protocol.Envelope, data any) ([]byte, error) {
	env.Data = nil
	head, err := json.Marshal(env)
	if err != nil || data == nil {
		return head, err
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", env.Type, err)
	}
	frame := make([]byte, 0, len(head)+len(`,"data":`)+len(raw))
	frame = append(frame, head[:len(head)-1]...) // without its closing brace
	frame = append(frame, `,"data":`...)
	frame = append(frame, raw...)
	return append(frame, '}'), nil
}

// decodeData decodes an envelope's payload. Unknown fields are ignored (the
// protocol only grows); an absent payload decodes to T's zero value.
func decodeData[T any](env protocol.Envelope) (T, error) {
	var out T
	if len(env.Data) == 0 || string(env.Data) == "null" {
		return out, nil
	}
	if err := json.Unmarshal(env.Data, &out); err != nil {
		return out, newError(protocol.ErrorCodeInvalid, "malformed %s: %v", env.Type, err)
	}
	return out, nil
}

// newServiceStatus builds a service:status snapshot of the supervised service
// the station knows as taskID.
func newServiceStatus(taskID string, snapshot model.ServiceSnapshot) protocol.ServiceStatus {
	instances := make([]protocol.ServiceInstance, 0, len(snapshot.Instances))
	for _, inst := range snapshot.Instances {
		item := protocol.ServiceInstance{
			Index:        inst.Index,
			State:        protocol.ServiceInstanceState(inst.State),
			StartedAt:    unixMs(inst.StartedAt),
			RestartCount: inst.RestartCount,
			StartFails:   inst.StartFails,
			LastExitCode: inst.LastExitCode,
			// A service run's execution id is its run id (executionIDOf).
			ExecutionID: inst.RunID,
		}
		if inst.Pid != 0 {
			item.Pid = &inst.Pid
		}
		instances = append(instances, item)
	}
	return protocol.ServiceStatus{
		TaskID:    taskID,
		State:     protocol.ServiceState(snapshot.State),
		Instances: instances,
	}
}

// unixMs is t as the protocol's Unix milliseconds, 0 for none.
func unixMs(t *time.Time) int64 {
	if t == nil || t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// toAgentStats maps the daemon's host snapshot onto agent:stats.
// Without a host backend the memory is the daemon's own heap, so it's left out.
func toAgentStats(s model.SystemStats, uptime time.Duration) protocol.AgentStats {
	out := protocol.AgentStats{CpuCores: s.CPUCores, UptimeMs: uptime.Milliseconds()}
	if s.HostMetrics {
		out.Load1 = &s.Load1
		out.MemTotalBytes, out.MemUsedBytes = int64(s.MemTotal), int64(s.MemUsed)
	}
	return out
}
