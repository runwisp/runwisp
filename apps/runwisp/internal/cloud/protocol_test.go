// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"errors"
	"sort"
	"testing"

	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamEnumFromString(t *testing.T) {
	tests := []struct {
		in   string
		want protocol.Stream
	}{
		{"stdout", protocol.StreamStdout},
		{"stderr", protocol.StreamStderr},
		{"system", protocol.StreamSystem},
		{"", protocol.StreamStdout}, // default fallback
		{"unknown", protocol.StreamStdout},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, streamEnumFromString(tt.in))
		})
	}
}

func TestLinesItemStreamFromString(t *testing.T) {
	tests := []struct {
		in   string
		want protocol.LinesItemStream
	}{
		{"stdout", protocol.LinesItemStreamStdout},
		{"stderr", protocol.LinesItemStreamStderr},
		{"system", protocol.LinesItemStreamSystem},
		{"", protocol.LinesItemStreamStdout}, // default fallback
		{"unknown", protocol.LinesItemStreamStdout},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			assert.Equal(t, tt.want, linesItemStreamFromString(tt.in))
		})
	}
}

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

// Adding fields to an existing cloud→daemon message must stay safe: the daemon
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

func TestEncodeOutboundMessage_RoundTrip(t *testing.T) {
	out, err := EncodeOutboundMessage(NewPingMessage(nil))
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
	})
	ping := NewPingMessage(provider())
	require.NotNil(t, ping.SystemStats)
	assert.Equal(t, 12.5, ping.SystemStats.CpuUsage)
	assert.Equal(t, int(16<<30), ping.SystemStats.MemTotal)
	assert.Equal(t, "linux", ping.SystemStats.Os)
	assert.Equal(t, "amd64", ping.SystemStats.Arch)
	assert.Equal(t, 8, ping.SystemStats.CpuCores)

	// Identity-only / path-leaking fields are intentionally not on the wire.
	out, err := EncodeOutboundMessage(ping)
	require.NoError(t, err)
	assert.NotContains(t, string(out), "/secret/path")
}

func TestMakeSystemStatsProvider_NilSource(t *testing.T) {
	assert.Nil(t, makeSystemStatsProvider(nil))
}
