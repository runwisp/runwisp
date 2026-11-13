// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"encoding/json"
	"errors"
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
		[]string{model.ServiceRunning, model.ServiceDegraded, model.ServiceStopped, model.ServiceFatal},
		enumStrings(protocol.ServiceStateValues))
	assert.ElementsMatch(t,
		[]string{model.ServiceInstanceRunning, model.ServiceInstanceRestarting, model.ServiceInstanceStopped, model.ServiceInstanceFatal},
		enumStrings(protocol.ServiceInstanceStateValues))
	assert.ElementsMatch(t,
		[]string{logutil.StreamStdout, logutil.StreamStderr, logutil.StreamSystem},
		enumStrings(protocol.LogStreamValues))
}

func TestResultData_FlattensFieldsNextToOk(t *testing.T) {
	raw, err := resultData(protocol.LogSubscribeResult{NextLine: 7, Eof: true}, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":true,"nextLine":7,"eof":true}`, string(raw))

	raw, err = resultData(nil, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":true}`, string(raw))
}

func TestResultData_ErrorCarriesCodeAndRetryable(t *testing.T) {
	raw, err := resultData(nil, newError(protocol.ErrorCodeNotFound, "execution %s not found", "e1"))
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":false,"error":{"code":"not_found","message":"execution e1 not found","retryable":false}}`, string(raw))

	// Anything that isn't a StationError is an internal, retryable failure.
	raw, err = resultData(nil, errors.New("disk on fire"))
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":false,"error":{"code":"internal","message":"disk on fire","retryable":true}}`, string(raw))
}

func TestDecodeResult_MapsRefusalToStationError(t *testing.T) {
	env := protocol.Envelope{Type: "result", Data: json.RawMessage(`{"ok":false,"error":{"code":"busy","message":"later","retryable":true}}`)}
	_, err := decodeResult[protocol.UploadRequestResult](env)
	se, ok := errors.AsType[*StationError](err)
	require.True(t, ok)
	assert.Equal(t, protocol.ErrorCodeBusy, se.Code)
	assert.True(t, se.Retryable)

	env.Data = json.RawMessage(`{"ok":true,"uploads":[{"url":"https://u","method":"PUT","key":"k"}]}`)
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
	assert.Equal(t, "timeout", u.Reason, "the reason goes out as the daemon's EndReason")
	assert.Equal(t, new(true), u.IsFailure)
	assert.Equal(t, new(-1), u.ExitCode)
	assert.Equal(t, &ended, u.FinishedAt)

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
