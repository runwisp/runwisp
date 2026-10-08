// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"encoding/json"
	"testing"

	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProtocolEnumsKeepUnknownValues pins forward-compat rule 4 in
// asyncapi.yaml: a value this daemon doesn't know must decode as itself, never
// as a known (semantically loaded) zero value such as "running" or "start".
func TestProtocolEnumsKeepUnknownValues(t *testing.T) {
	var update protocol.ExecutionUpdateMessage
	require.NoError(t, json.Unmarshal([]byte(`{"type":"execution:update","executionId":"e","status":"paused"}`), &update))
	assert.Equal(t, protocol.ExecutionStatus("paused"), update.Status)

	var control protocol.ServiceControlMessage
	require.NoError(t, json.Unmarshal([]byte(`{"type":"service:control","taskId":"t","action":"pause"}`), &control))
	assert.Equal(t, protocol.Action("pause"), control.Action)

	out, err := json.Marshal(protocol.LogLineEntry{N: 1, Stream: protocol.StreamStderr, Text: "x"})
	require.NoError(t, err)
	assert.JSONEq(t, `{"n":1,"stream":"stderr","text":"x"}`, string(out))
}
