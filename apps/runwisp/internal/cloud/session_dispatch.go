// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package cloud

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	// systemStats, when set, returns the live host snapshot piggybacked on each
	// heartbeat. nil (no provider wired) sends a plain ping.
	systemStats func() *protocol.SystemStats
}

// currentSystemStats reads the snapshot provider, tolerating a nil provider so
// the heartbeat degrades to a plain ping rather than panicking.
func (sr *sessionRunner) currentSystemStats() *protocol.SystemStats {
	if sr.systemStats == nil {
		return nil
	}
	return sr.systemStats()
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
		// Server-initiated close. Treated as clean (reconnect with a fresh
		// backoff), but it must never be silent: an idle-kill loop on the
		// cloud side looks exactly like this.
		slog.Warn("cloud closed the session", "status", status.String(), "detail", firstErr.Error())
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
			if err := sendMessage(session, NewPingMessage(sr.currentSystemStats())); err != nil {
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
		if errors.Is(err, ErrUnsupportedMessageType) {
			// Forward-compat: cloud sent a message type this daemon version
			// doesn't implement yet. Ignore it cleanly — the session survives
			// and we send NO error frame. A future feature is not a validation
			// failure, and replying VALIDATION_ERROR would only generate a
			// cloud-side WARN + diagnostic event for every such frame. See the
			// forward-compat contract atop packages/asyncapi/asyncapi.yaml.
			slog.Debug("ignoring unsupported inbound message type", "error", err.Error())
			return nil
		}
		// A known type that failed to decode is a genuine protocol violation
		// (malformed JSON for a message we do understand) — surface it.
		sr.sendProtocolError(session, CloudErrorKindValidation, "Failed to parse message", "", "")
		return err
	}

	switch message := decoded.(type) {
	case protocol.PongMessage:
		return nil
	case protocol.ProtocolErrorMessage:
		slog.Info("cloud protocol error", "code", message.Code, "message", message.Message, "requestId", message.RequestID, "executionId", message.ExecutionID)
		return nil
	case protocol.ExecutionDispatchMessage:
		executionID := strings.TrimSpace(message.Execution.ExecutionID)
		ack := func() {
			if ackErr := sendMessage(session, NewExecutionAckMessage(executionID)); ackErr != nil {
				slog.Info("failed to send execution ack", "executionId", executionID, "error", ackErr.Error())
			}
		}
		if dispatchErr := sr.handler.HandleExecutionDispatch(ctx, message, ack); dispatchErr != nil {
			sr.sendProtocolError(session, classifyErrorKind(dispatchErr), dispatchErr.Error(), "", executionID)
		}
		return nil
	case protocol.ExecutionStopMessage:
		return sr.reportIfErr(session, sr.handler.HandleExecutionStop(ctx, message), strings.TrimSpace(message.ExecutionID))
	case protocol.LogReplayRequestMessage:
		response, responseErr := sr.handler.HandleLogReplayRequest(ctx, message)
		if responseErr != nil {
			// One frame per request: report the error and stop — don't also
			// send the (empty) response chunk.
			sr.sendProtocolError(session, classifyErrorKind(responseErr), responseErr.Error(), message.ID, strings.TrimSpace(message.ExecutionID))
			return nil
		}
		return sendMessage(session, response)
	case protocol.LogSearchRequestMessage:
		response, responseErr := sr.handler.HandleLogSearchRequest(ctx, message)
		if responseErr != nil {
			sr.sendProtocolError(session, classifyErrorKind(responseErr), responseErr.Error(), message.ID, strings.TrimSpace(message.ExecutionID))
			return nil
		}
		return sendMessage(session, response)
	case protocol.LogListenMessage:
		return sr.reportIfErr(session, sr.handler.HandleLogListen(message), strings.TrimSpace(message.ExecutionID))
	case protocol.LogStopMessage:
		sr.handler.HandleLogStop(message)
		return nil
	case protocol.AgentRestartMessage:
		return sr.reportIfErr(session, sr.handler.HandleAgentRestart(), "")
	case protocol.ServiceApplyMessage:
		return sr.reportIfErr(session, sr.handler.HandleServiceApply(message), "")
	case protocol.ServiceControlMessage:
		return sr.reportIfErr(session, sr.handler.HandleServiceControl(message), "")
	case protocol.ServiceRemoveMessage:
		return sr.reportIfErr(session, sr.handler.HandleServiceRemove(message), "")
	case protocol.AuthResultMessage:
		if !message.Success {
			return &CloudError{Kind: CloudErrorKindAuth, Message: message.Error}
		}
		return nil
	default:
		// Defensive: unknown *types* are filtered upstream by
		// DecodeInboundMessage (ErrUnsupportedMessageType, handled above), so
		// this arm is only reachable if a decoder was added to inboundDecoders
		// without a matching case here — a coding error, not a wire event. Log
		// it; do NOT send an error frame (it isn't the daemon's fault).
		slog.Warn("decoded message type has no dispatch case", "type", fmt.Sprintf("%T", decoded))
		return nil
	}
}

func (sr *sessionRunner) sendProtocolError(session *wsSession, kind CloudErrorKind, message, requestID, executionID string) {
	errorMessage := NewProtocolErrorMessage(string(kind), message, requestID, executionID)
	if err := sendMessage(session, errorMessage); err != nil {
		slog.Info("failed to send protocol error", "error", err.Error())
	}
}

// reportIfErr forwards a non-nil inbound-handler error to the cloud peer as a
// protocol-error frame and always returns nil: a handler failure is reported,
// not fatal — the session survives. Shared by the fire-and-forget inbound arms
// (those with no response of their own to send). executionID scopes the error
// to a specific execution when the message carried one; "" otherwise.
func (sr *sessionRunner) reportIfErr(session *wsSession, err error, executionID string) error {
	if err != nil {
		sr.sendProtocolError(session, classifyErrorKind(err), err.Error(), "", executionID)
	}
	return nil
}
