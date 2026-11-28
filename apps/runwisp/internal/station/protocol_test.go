// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func enumStrings[T ~string](vals []T) []string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = string(v)
	}
	return out
}

// The wire enums are mirrored by hand against the daemon's model constants;
// nothing generated links them. A rename on one side alone would send values
// the station can't map, so these pin each pair.
func TestProtocolEnumsMatchModel(t *testing.T) {
	assert.ElementsMatch(t,
		[]string{string(model.BackoffConstant), string(model.BackoffLinear), string(model.BackoffExponential)},
		enumStrings(protocol.BackoffValues))
	assert.ElementsMatch(t,
		[]string{string(model.PolicyQueue), string(model.PolicySkip), string(model.PolicyKill)},
		enumStrings(protocol.OverlapPolicyValues))
	assert.ElementsMatch(t,
		// removed is wire-only: the model has no state for a service that's gone.
		[]string{model.ServiceRunning, model.ServiceDegraded, model.ServiceStopped, model.ServiceFatal, string(protocol.ServiceStateRemoved)},
		enumStrings(protocol.ServiceStateValues))
	assert.ElementsMatch(t,
		[]string{model.ServiceInstanceRunning, model.ServiceInstanceRestarting, model.ServiceInstanceStopped, model.ServiceInstanceFatal},
		enumStrings(protocol.ServiceInstanceStateValues))
	assert.ElementsMatch(t,
		[]string{logutil.StreamStdout, logutil.StreamStderr, logutil.StreamSystem},
		enumStrings(protocol.LogStreamValues))
	reasons := []string{}
	for _, r := range model.AllEndReasons {
		reasons = append(reasons, string(r))
	}
	assert.ElementsMatch(t, reasons, enumStrings(protocol.EndReasonValues))
}

func TestErrorInfo_CarriesCodeAndRetryable(t *testing.T) {
	frame, err := encodeFrame(protocol.Envelope{Type: "result", ReplyTo: "s1", Error: errorInfo(newError(protocol.ErrorCodeNotFound, "execution %s not found", "e1"))}, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"type":"result","replyTo":"s1","error":{"code":"not_found","message":"execution e1 not found","retryable":false}}`, string(frame))

	// Anything that isn't a StationError is an internal, retryable failure.
	assert.Equal(t, &protocol.ErrorInfo{Code: protocol.ErrorCodeInternal, Message: "disk on fire", Retryable: true}, errorInfo(errors.New("disk on fire")))
}

func TestDecodeResult_MapsRefusalToStationError(t *testing.T) {
	env := protocol.Envelope{Type: "result", Error: &protocol.ErrorInfo{Code: protocol.ErrorCodeBusy, Message: "later", Retryable: true}}
	_, err := decodeResult[protocol.UploadRequestResult](env)
	se, ok := errors.AsType[*StationError](err)
	require.True(t, ok)
	assert.Equal(t, protocol.ErrorCodeBusy, se.Code)
	assert.True(t, se.Retryable)

	env.Error, env.Data = nil, json.RawMessage(`{"uploads":[{"url":"https://u","method":"PUT","key":"k"}]}`)
	res, err := decodeResult[protocol.UploadRequestResult](env)
	require.NoError(t, err)
	require.Len(t, res.Uploads, 1)
	assert.Equal(t, "k", res.Uploads[0].Key)
}

func TestExecutionUpdate_FinishedCarriesOutcome(t *testing.T) {
	started := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	ended := started.Add(time.Minute)
	reason := model.ReasonTimeout
	run := &model.Run{ExecutionID: new("e1"), Status: model.PhaseEnded, EndReason: &reason, IsFailure: true, ExitCode: -1, StartedAt: &started, EndedAt: &ended}

	u := executionUpdate(run)
	require.NotNil(t, u)
	assert.Equal(t, protocol.ExecutionStatusFinished, u.Status)
	assert.Equal(t, protocol.EndReasonTimeout, u.Reason, "the reason goes out as the daemon's EndReason")
	assert.Equal(t, new(true), u.IsFailure)
	assert.Equal(t, new(-1), u.ExitCode)
	assert.Equal(t, ended.UnixMilli(), u.FinishedAt)

	run.Status, run.EndReason = model.PhaseRunning, nil
	u = executionUpdate(run)
	assert.Equal(t, protocol.ExecutionStatusRunning, u.Status)
	assert.Nil(t, u.IsFailure)

	u = executionUpdate(&model.Run{ID: "r1", TaskName: "web", InstanceIndex: 2, Status: model.PhaseRunning})
	assert.Equal(t, &protocol.ExecutionUpdate{ExecutionID: "r1", TaskID: "web", Instance: new(2), Status: protocol.ExecutionStatusRunning}, u,
		"a service instance's run goes by its run id, naming its task and instance")
}

func TestTracker_InventoryCoversLiveAndUnacked(t *testing.T) {
	tr := NewExecutionTracker()
	require.True(t, tr.Reserve("accepted"))
	require.True(t, tr.Reserve("running"))
	tr.TrackRunning("running")
	assert.False(t, tr.Reserve("running"), "a known execution can't be reserved twice")

	inv := tr.Inventory([]storage.OutboxExecution{{ExecutionID: "done"}, {ExecutionID: "running"}})
	assert.Equal(t, []protocol.InventoryEntry{
		{ExecutionID: "accepted", Status: protocol.ExecutionStatusAccepted},
		{ExecutionID: "done", Status: protocol.ExecutionStatusFinished},
		{ExecutionID: "running", Status: protocol.ExecutionStatusRunning},
	}, inv)
	assert.NotNil(t, NewExecutionTracker().Inventory(nil), "an empty inventory is [] on the wire")
}

// A hello past maxInventory keeps every live execution and the oldest
// finished ones.
func TestTracker_InventoryBounded(t *testing.T) {
	tr := NewExecutionTracker()
	require.True(t, tr.Reserve("zz-live"))
	outbox := make([]storage.OutboxExecution, maxInventory+10)
	for i := range outbox {
		outbox[i].ExecutionID = fmt.Sprintf("f%05d", i)
	}
	inv := tr.Inventory(outbox)
	require.Len(t, inv, maxInventory)
	assert.Equal(t, "f00000", inv[0].ExecutionID)
	assert.Equal(t, fmt.Sprintf("f%05d", maxInventory-2), inv[maxInventory-2].ExecutionID)
	assert.Equal(t, protocol.InventoryEntry{ExecutionID: "zz-live", Status: protocol.ExecutionStatusAccepted}, inv[maxInventory-1])
}

// encodeFrame's spliced envelope is the one json.Marshal builds.
func TestEncodeFrame_MatchesMarshal(t *testing.T) {
	for _, c := range []struct {
		env  protocol.Envelope
		data any
	}{
		{protocol.Envelope{Type: "ping"}, nil},
		{protocol.Envelope{Type: "result", ReplyTo: "s<1>"}, protocol.UploadRequestResult{Uploads: []protocol.UploadTarget{}}},
		{protocol.Envelope{Type: "result", ReplyTo: "s9", Error: &protocol.ErrorInfo{Code: protocol.ErrorCodeBusy}}, nil},
		{protocol.Envelope{Type: "log:lines", ReplyTo: "s2"}, protocol.LogLines{Lines: []protocol.LogLine{{N: 1, Text: "a \"b\"  "}}}},
		{protocol.Envelope{Type: "execution:stop", ID: "d\"3"}, (*protocol.ExecutionStop)(nil)},
	} {
		got, err := encodeFrame(c.env, c.data)
		require.NoError(t, err)
		want := c.env
		if c.data != nil {
			want.Data, _ = json.Marshal(c.data)
		}
		wantRaw, _ := json.Marshal(want)
		assert.Equal(t, string(wantRaw), string(got), c.env.Type)
	}
}

// The station knows a service it declared by its own taskId and a TOML one by
// its name, in service:status, execution:update and tasks:sync alike.
func TestServiceTaskID(t *testing.T) {
	declared := &model.Task{Name: "station-01JC8Y7W", Kind: model.KindService, StationTaskID: "01JC8Y7W"}
	toml := &model.Task{Name: "station-web", Kind: model.KindService}
	client := &Client{deps: Dependencies{TaskManager: &fakeTaskRunner{tasks: map[string]*model.Task{declared.Name: declared, toml.Name: toml}}}}

	assert.Equal(t, "01JC8Y7W", client.serviceTaskID("station-01JC8Y7W"))
	assert.Equal(t, "station-web", client.serviceTaskID("station-web"), "a TOML service keeps its name, prefix or not")
	assert.Equal(t, "01JC8Y9A", client.serviceTaskID("station-01JC8Y9A"), "one not re-declared since a restart")
	assert.Equal(t, "web", client.serviceTaskID("web"))

	tasks, services := client.snapshotForSync()
	assert.Equal(t, []string{"01JC8Y7W"}, services)
	assert.Contains(t, tasks, "station-web")
	assert.NotContains(t, tasks, "station-01JC8Y7W")
}

func TestToAgentStats_HostMetricsOnly(t *testing.T) {
	stats := toAgentStats(model.SystemStats{HostMetrics: true, Load1: 2.5, MemTotal: 100, MemUsed: 40, CPUCores: 2}, time.Second)
	assert.Equal(t, protocol.AgentStats{Load1: new(2.5), MemTotalBytes: 100, MemUsedBytes: 40, CpuCores: 2, UptimeMs: 1000}, stats)
}
