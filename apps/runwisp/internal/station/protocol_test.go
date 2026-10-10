// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"encoding/json"
	"errors"
	"sort"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/apps/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeStrict_TrailingDataIsRejected(t *testing.T) {
	// decoder.More() must surface trailing JSON payloads as an error so the
	// peer can't smuggle a second envelope into a single frame.
	var msg protocol.PongMessage
	err := decodeStrict([]byte(`{"type":"pong"}{"extra":1}`), &msg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trailing")
}

func TestDecodeStrict_InvalidJSON(t *testing.T) {
	var msg protocol.PongMessage
	err := decodeStrict([]byte(`{not json`), &msg)
	require.Error(t, err)
}

func TestDecodeStrict_ValidJSON(t *testing.T) {
	var msg protocol.PongMessage
	err := decodeStrict([]byte(`{"type":"pong"}`), &msg)
	require.NoError(t, err)
	assert.Equal(t, "pong", msg.Type)
}

func TestDecodeInboundMessage_UnsupportedType(t *testing.T) {
	_, err := DecodeInboundMessage([]byte(`{"type":"never:gonna:happen"}`))
	require.Error(t, err)
	// Must be the typed sentinel, not a generic decode failure — the caller
	// keys off errors.Is to ignore the frame cleanly instead of tearing the
	// session down or replying VALIDATION_ERROR.
	assert.True(t, errors.Is(err, ErrUnsupportedMessageType), "want ErrUnsupportedMessageType, got %v", err)
	assert.Contains(t, err.Error(), "never:gonna:happen", "error should name the offending type")
}

// Adding fields to an existing station→daemon message must stay safe: the daemon
// decodes a known type with extra unknown fields without error (it does NOT use
// DisallowUnknownFields — only decodeStrict's trailing-JSON check). This locks
// rule 1 of the forward-compat contract. If a future regen or refactor turns on
// strict field checking, this test fails loudly.
func TestDecodeInboundMessage_UnknownFieldsTolerated(t *testing.T) {
	msg, err := DecodeInboundMessage([]byte(`{"type":"execution:stop","executionId":"e1","reason":"manual","futureField":{"nested":true}}`))
	require.NoError(t, err)
	stop, ok := msg.(protocol.ExecutionStopMessage)
	require.True(t, ok)
	assert.Equal(t, "e1", stop.ExecutionID)
	assert.Equal(t, "manual", stop.Reason)
}

// The feature set advertised at auth (X-Runner-Protocol-Features) must equal
// the inbound decoder keys exactly — that equivalence is the whole point of
// deriving it from inboundDecoders, so the daemon never advertises a type it
// can't handle (or omits one it can). If a decoder is added/removed without the
// advertisement following, this fails.
func TestSupportedInboundTypes_MatchesDecoders(t *testing.T) {
	got := supportedInboundTypes()
	want := make([]string, 0, len(inboundDecoders))
	for k := range inboundDecoders {
		want = append(want, k)
	}
	assert.ElementsMatch(t, want, got)
	// And it must be sorted (stable header value across reconnects).
	assert.True(t, sort.StringsAreSorted(got), "advertised feature set must be sorted, got %v", got)
}

func TestDecodeInboundMessage_MalformedEnvelope(t *testing.T) {
	_, err := DecodeInboundMessage([]byte(`{`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "envelope")
}

func TestDecodeInboundMessage_KnownTypeRoundtrips(t *testing.T) {
	msg, err := DecodeInboundMessage([]byte(`{"type":"pong"}`))
	require.NoError(t, err)
	pong, ok := msg.(protocol.PongMessage)
	require.True(t, ok)
	assert.Equal(t, "pong", pong.Type)
}

func TestPingMessage_OmitsEmptySystemStats(t *testing.T) {
	out, err := json.Marshal(NewPingMessage(nil))
	require.NoError(t, err)
	assert.Contains(t, string(out), `"type":"ping"`)
	// A plain ping must not carry an empty systemStats object — the field is
	// add-only and omitted when no snapshot is attached.
	assert.NotContains(t, string(out), "systemStats")
}

func TestNewPingMessage_CarriesSystemStats(t *testing.T) {
	provider := makeSystemStatsProvider(func() model.SystemStats {
		return model.SystemStats{
			CPUUsage: 12.5, MemUsage: 40, MemTotal: 16 << 30, MemUsed: 6 << 30,
			CPUCores: 8, Uptime: "1h", Version: "1.2.3", Host: "box",
			OS: "linux", Arch: "amd64", Name: "runwisp", WorkDir: "/secret/path",
		}
	}, func() string { return "storage-nas" })
	ping := NewPingMessage(provider())
	require.NotNil(t, ping.SystemStats)
	assert.Equal(t, 12.5, ping.SystemStats.CpuUsage)
	assert.Equal(t, int64(16<<30), ping.SystemStats.MemTotal)
	assert.Equal(t, "linux", ping.SystemStats.Os)
	assert.Equal(t, "amd64", ping.SystemStats.Arch)
	assert.Equal(t, 8, ping.SystemStats.CpuCores)
	assert.Equal(t, "storage-nas", ping.SystemStats.InstanceName)

	// Identity-only / path-leaking fields are intentionally not on the wire.
	out, err := json.Marshal(ping)
	require.NoError(t, err)
	assert.NotContains(t, string(out), "/secret/path")
}

func TestMakeSystemStatsProvider_NilSource(t *testing.T) {
	assert.Nil(t, makeSystemStatsProvider(nil, nil))
}

func TestNewExecutionAckMessage(t *testing.T) {
	ack := NewExecutionAckMessage("exec-123")
	assert.Equal(t, "execution:ack", ack.Type)
	assert.Equal(t, "exec-123", ack.ExecutionID)
	// Envelope fields are populated (version + timestamp), not left zero.
	assert.NotZero(t, ack.ProtocolVersion)
	assert.NotEmpty(t, ack.SentAt)
}

func TestNewServiceStatusMessage(t *testing.T) {
	exit := 137
	snapshot := model.ServiceSnapshot{
		TaskName:         "station-svc",
		State:            model.ServiceRunning,
		DesiredInstances: 3,
		RunningInstances: 2,
		Instances: []model.ServiceInstanceStatus{
			{Index: 0, State: model.ServiceInstanceRunning, Pid: 42, RestartCount: 1},
			// Second instance carries an exit code but no pid.
			{Index: 1, State: "fatal", RestartCount: 4, LastExitCode: &exit},
		},
	}

	msg := NewServiceStatusMessage(snapshot)
	assert.Equal(t, "service:status", msg.Type)
	assert.Equal(t, "station-svc", msg.TaskID)
	assert.Equal(t, protocol.ServiceStateRunning, msg.State)
	assert.Equal(t, 3, msg.DesiredInstances)
	assert.Equal(t, 2, msg.RunningInstances)

	require.Len(t, msg.Instances, 2)
	assert.Equal(t, 0, msg.Instances[0].Index)
	assert.Equal(t, protocol.ServiceInstanceStateRunning, msg.Instances[0].State)
	assert.Equal(t, 42, *msg.Instances[0].Pid)
	// pid/lastExitCode unset on the wire when the daemon reports none.
	assert.Nil(t, msg.Instances[1].Pid)
	assert.Nil(t, msg.Instances[0].LastExitCode)
	assert.Equal(t, 137, *msg.Instances[1].LastExitCode)
	assert.Equal(t, 4, msg.Instances[1].RestartCount)
}
