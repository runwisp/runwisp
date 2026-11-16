// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package station

import (
	"context"

	"github.com/runwisp/runwisp/apps/runwisp/internal/generated/protocol"
)

// command is how the daemon carries out one station command. run returns the
// result's fields (nil for none, or a followUp) and its error. An async
// command runs on its own goroutine so a long one (a log subscription, an
// upgrade download) never holds up the frames behind it; the others run in
// arrival order on the read loop, which keeps state changes ordered.
type command struct {
	async bool
	run   func(ctx context.Context, s *session, env protocol.Envelope) (any, error)
}

// commands are the station commands this daemon understands. Anything else
// with an id is answered unsupported.
var commands = map[string]command{
	typeSessionUpdate: {run: func(_ context.Context, s *session, env protocol.Envelope) (any, error) {
		u, err := decodeData[protocol.SessionUpdate](env)
		if err == nil {
			s.updateParams(u)
		}
		return nil, err
	}},
	typeExecDispatch: {run: func(ctx context.Context, s *session, env protocol.Envelope) (any, error) {
		d, err := decodeData[protocol.ExecutionDispatch](env)
		if err != nil {
			return nil, err
		}
		return nil, s.client.handler.HandleExecutionDispatch(ctx, d)
	}},
	typeExecStop: {run: func(ctx context.Context, s *session, env protocol.Envelope) (any, error) {
		stop, err := decodeData[protocol.ExecutionStop](env)
		if err != nil {
			return nil, err
		}
		return nil, s.client.handler.HandleExecutionStop(ctx, stop)
	}},
	typeServiceApply: {run: func(_ context.Context, s *session, env protocol.Envelope) (any, error) {
		svc, err := decodeData[protocol.ServiceApply](env)
		if err != nil {
			return nil, err
		}
		return nil, s.client.handler.HandleServiceApply(svc)
	}},
	typeServiceControl: {run: func(_ context.Context, s *session, env protocol.Envelope) (any, error) {
		ctl, err := decodeData[protocol.ServiceControl](env)
		if err != nil {
			return nil, err
		}
		return nil, s.client.handler.HandleServiceControl(ctl)
	}},
	typeServiceRemove: {run: func(_ context.Context, s *session, env protocol.Envelope) (any, error) {
		rm, err := decodeData[protocol.ServiceRemove](env)
		if err != nil {
			return nil, err
		}
		return nil, s.client.handler.HandleServiceRemove(rm)
	}},
	typeLogSubscribe: {async: true, run: func(ctx context.Context, s *session, env protocol.Envelope) (any, error) {
		sub, err := decodeData[protocol.LogSubscribe](env)
		if err != nil {
			return nil, err
		}
		return s.streamLog(ctx, env.ID, sub)
	}},
	typeLogSearch: {async: true, run: func(ctx context.Context, s *session, env protocol.Envelope) (any, error) {
		q, err := decodeData[protocol.LogSearch](env)
		if err != nil {
			return nil, err
		}
		return s.client.searchLog(ctx, q, s.maxFrameBytes())
	}},
	typeAgentRestart: {run: func(_ context.Context, s *session, _ protocol.Envelope) (any, error) {
		return s.client.restart()
	}},
	typeAgentUpgrade: {async: true, run: func(ctx context.Context, s *session, env protocol.Envelope) (any, error) {
		u, err := decodeData[protocol.AgentUpgrade](env)
		if err != nil {
			return nil, err
		}
		return s.client.upgrade(ctx, u.Version)
	}},
	typeAgentCredentials: {run: func(ctx context.Context, s *session, env protocol.Envelope) (any, error) {
		c, err := decodeData[protocol.AgentCredentials](env)
		if err != nil {
			return nil, err
		}
		return nil, s.client.rotateToken(ctx, c.Token)
	}},
}
