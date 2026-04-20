// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"context"

	"github.com/coder/websocket"
	"github.com/runwisp/runwisp/internal/generated/protocol"
)

func readAuthResult(ctx context.Context, connection *websocket.Conn) (protocol.AuthResultMessage, error) {
	readCtx, cancel := context.WithTimeout(ctx, authReadTimeout)
	defer cancel()

	_, payload, err := connection.Read(readCtx)
	if err != nil {
		return protocol.AuthResultMessage{}, &CloudError{
			Kind:    CloudErrorKindTransient,
			Message: "failed to read auth result",
			Err:     err,
		}
	}

	message, err := DecodeInboundMessage(payload)
	if err != nil {
		return protocol.AuthResultMessage{}, &CloudError{
			Kind:    CloudErrorKindValidation,
			Message: "failed to parse auth result",
			Err:     err,
		}
	}

	authResult, ok := message.(protocol.AuthResultMessage)
	if !ok {
		return protocol.AuthResultMessage{}, &CloudError{
			Kind:    CloudErrorKindValidation,
			Message: "first websocket message must be auth:result",
		}
	}

	return authResult, nil
}
