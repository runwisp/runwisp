// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"log/slog"

	"github.com/coder/websocket"
	"github.com/runwisp/runwisp/internal/generated/protocol"
)

// sessionRunner manages the active WebSocket session loops (read, write,
// heartbeat, watchdog) and inbound message dispatch. Separated from Client
// to isolate session-level concerns from connection lifecycle.
type sessionRunner struct {
	handler *InboundHandler
}

func (sr *sessionRunner) run(ctx context.Context, session *wsSession) error {
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	errChan := make(chan error, 4)
	var waitGroup sync.WaitGroup

	runLoop := func(loop func(context.Context, *wsSession) error) {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			if err := loop(sessionCtx, session); err != nil {
				select {
				case errChan <- err:
				default:
				}
			}
		}()
	}

	runLoop(sr.readLoop)
	runLoop(sr.writeLoop)
	runLoop(sr.heartbeatLoop)
	runLoop(sr.watchdogLoop)

	var firstErr error
	select {
	case <-ctx.Done():
		firstErr = nil
	case firstErr = <-errChan:
	}

	cancel()
	if err := session.conn.Close(websocket.StatusNormalClosure, "reconnect"); err != nil {
		slog.Info("failed to close session", "err", err)
	}
	waitGroup.Wait()

	if errors.Is(firstErr, context.Canceled) {
		return nil
	}

	status := websocket.CloseStatus(firstErr)
	if status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway {
		return nil
	}

	return firstErr
}

func (sr *sessionRunner) readLoop(ctx context.Context, session *wsSession) error {
	session.conn.SetReadLimit(maxInboundMessageSize)
	for {
		_, payload, err := session.conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("read loop: %w", err)
		}

		session.lastReceived.Store(time.Now().UnixMilli())

		if err := sr.handleInboundPayload(ctx, session, payload); err != nil {
			slog.Info("failed to handle inbound message", "error", err.Error())
		}
	}
}

func (sr *sessionRunner) writeLoop(ctx context.Context, session *wsSession) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case payload := <-session.outbound:
			writeCtx, cancelWrite := context.WithTimeout(ctx, writeTimeout)
			err := session.conn.Write(writeCtx, websocket.MessageText, payload)
			cancelWrite()
			if err != nil {
				return fmt.Errorf("write loop: %w", err)
			}
		}
	}
}

func (sr *sessionRunner) heartbeatLoop(ctx context.Context, session *wsSession) error {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := sendMessage(session, NewPingMessage()); err != nil {
				return fmt.Errorf("heartbeat: %w", err)
			}
		}
	}
}

func (sr *sessionRunner) watchdogLoop(ctx context.Context, session *wsSession) error {
	ticker := time.NewTicker(watchdogInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			lastReceived := time.UnixMilli(session.lastReceived.Load())
			if time.Since(lastReceived) > heartbeatSilenceTimeout {
				return fmt.Errorf("heartbeat timeout exceeded")
			}
		}
	}
}

func (sr *sessionRunner) handleInboundPayload(ctx context.Context, session *wsSession, payload []byte) error {
	decoded, err := DecodeInboundMessage(payload)
	if err != nil {
		sr.sendProtocolError(session, CloudErrorKindValidation, "Failed to parse message", "")
		return err
	}

	switch message := decoded.(type) {
	case protocol.PongMessage:
		return nil
	case protocol.ProtocolErrorMessage:
		slog.Info("cloud protocol error", "code", message.Code, "message", message.Message, "requestId", message.RequestID)
		return nil
	case protocol.ExecutionDispatchMessage:
		if dispatchErr := sr.handler.HandleExecutionDispatch(ctx, message); dispatchErr != nil {
			sr.sendProtocolError(session, classifyErrorKind(dispatchErr), dispatchErr.Error(), "")
		}
		return nil
	case protocol.ExecutionStopMessage:
		if stopErr := sr.handler.HandleExecutionStop(ctx, message); stopErr != nil {
			sr.sendProtocolError(session, classifyErrorKind(stopErr), stopErr.Error(), "")
		}
		return nil
	case protocol.LogReplayRequestMessage:
		response, responseErr := sr.handler.HandleLogReplayRequest(ctx, message)
		if responseErr != nil {
			sr.sendProtocolError(session, classifyErrorKind(responseErr), responseErr.Error(), message.ID)
		}
		if sendErr := sendMessage(session, response); sendErr != nil {
			return sendErr
		}
		return nil
	case protocol.LogListenMessage:
		if listenErr := sr.handler.HandleLogListen(message); listenErr != nil {
			sr.sendProtocolError(session, classifyErrorKind(listenErr), listenErr.Error(), "")
		}
		return nil
	case protocol.LogStopMessage:
		sr.handler.HandleLogStop(message)
		return nil
	case protocol.AuthResultMessage:
		if !message.Success {
			return &CloudError{Kind: CloudErrorKindAuth, Message: message.Error}
		}
		return nil
	default:
		sr.sendProtocolError(session, CloudErrorKindValidation, "Unsupported message type", "")
		return fmt.Errorf("unsupported message payload")
	}
}

func (sr *sessionRunner) sendProtocolError(session *wsSession, kind CloudErrorKind, message, requestID string) {
	errorMessage := NewProtocolErrorMessage(string(kind), message, requestID)
	if err := sendMessage(session, errorMessage); err != nil {
		slog.Info("failed to send protocol error", "error", err.Error())
	}
}
