// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"testing"

	"github.com/runwisp/runwisp/internal/generated/protocol"
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
	assert.Contains(t, err.Error(), "unsupported")
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
	out, err := EncodeOutboundMessage(NewPingMessage())
	require.NoError(t, err)
	assert.Contains(t, string(out), `"type":"ping"`)
}
