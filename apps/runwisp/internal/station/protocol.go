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
	typeSessionUpdate    = "session:update"
	typeAgentUpdate      = "agent:update"
	typeAgentStats       = "agent:stats"
	typeAgentRestart     = "agent:restart"
	typeAgentUpgrade     = "agent:upgrade"
	typeAgentCredentials = "agent:credentials"
	typeTasksSync        = "tasks:sync"
	typeExecDispatch     = "execution:dispatch"
	typeExecStop         = "execution:stop"
	typeExecUpdate       = "execution:update"
	typeLogSubscribe     = "log:subscribe"
	typeLogUnsubscribe   = "log:unsubscribe"
	typeLogLines         = "log:lines"
	typeLogSearch        = "log:search"
	typeUploadRequest    = "upload:request"
	typeServiceApply     = "service:apply"
	typeServiceControl   = "service:control"
	typeServiceRemove    = "service:remove"
	typeServiceStatus    = "service:status"
	typeServiceLogs      = "service:logs"
)

// encodeFrame builds one envelope. data may be nil for an empty payload.
func encodeFrame(typ, id, replyTo string, data any) ([]byte, error) {
	env := protocol.Envelope{Type: typ, ID: id, ReplyTo: replyTo, SentAt: time.Now().UTC().Format(time.RFC3339Nano)}
	if data != nil {
		raw, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("encode %s: %w", typ, err)
		}
		env.Data = raw
	}
	return json.Marshal(env)
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

// resultData is a result's payload: ok, error, and the command's own result
// fields (a struct, or nil) flattened next to them.
func resultData(fields any, err error) (json.RawMessage, error) {
	if err != nil {
		return json.Marshal(protocol.Result{Ok: false, Error: errorInfo(err)})
	}
	obj := map[string]json.RawMessage{}
	if fields != nil {
		raw, mErr := json.Marshal(fields)
		if mErr != nil {
			return nil, mErr
		}
		if mErr := json.Unmarshal(raw, &obj); mErr != nil {
			return nil, fmt.Errorf("result fields must be an object: %w", mErr)
		}
	}
	obj["ok"] = json.RawMessage("true")
	return json.Marshal(obj)
}

// newServiceStatus builds a service:status snapshot of a supervised service.
func newServiceStatus(snapshot model.ServiceSnapshot) protocol.ServiceStatus {
	instances := make([]protocol.ServiceInstance, 0, len(snapshot.Instances))
	for _, inst := range snapshot.Instances {
		item := protocol.ServiceInstance{
			Index:        inst.Index,
			State:        protocol.ServiceInstanceState(inst.State),
			StartedAt:    inst.StartedAt,
			RestartCount: inst.RestartCount,
			StartFails:   inst.StartFails,
			LastExitCode: inst.LastExitCode,
		}
		if inst.Pid != 0 {
			item.Pid = &inst.Pid
		}
		instances = append(instances, item)
	}
	return protocol.ServiceStatus{
		TaskID:           snapshot.TaskName,
		State:            protocol.ServiceState(snapshot.State),
		DesiredInstances: snapshot.DesiredInstances,
		RunningInstances: snapshot.RunningInstances,
		Instances:        instances,
	}
}

// toAgentStats maps the daemon's host snapshot onto agent:stats.
func toAgentStats(s model.SystemStats, uptime time.Duration) protocol.AgentStats {
	return protocol.AgentStats{
		CpuUsage:      s.CPUUsage,
		MemUsage:      s.MemUsage,
		MemTotalBytes: int64(s.MemTotal),
		MemUsedBytes:  int64(s.MemUsed),
		CpuCores:      s.CPUCores,
		UptimeMs:      uptime.Milliseconds(),
	}
}
