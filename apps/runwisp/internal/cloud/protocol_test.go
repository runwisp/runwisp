// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"testing"

	"github.com/runwisp/runwisp/internal/generated/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodeInboundMessageAuthResult(t *testing.T) {
	payload := []byte(`{"type":"auth:result","success":true,"connectionId":"conn-1"}`)

	decoded, err := DecodeInboundMessage(payload)
	require.NoError(t, err)

	message, ok := decoded.(protocol.AuthResultMessage)
	require.True(t, ok)
	assert.True(t, message.Success)
	assert.Equal(t, "conn-1", message.ConnectionID)
}
