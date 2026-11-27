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

// handle builds a command whose data decodes into T; malformed data is
// refused with invalid before fn runs. id is the command's id.
func handle[T any](async bool, fn func(s *session, ctx context.Context, id string, req T) (any, error)) command {
	return command{async: async, run: func(ctx context.Context, s *session, env protocol.Envelope) (any, error) {
		req, err := decodeData[T](env)
		if err != nil {
			return nil, err
		}
		return fn(s, ctx, env.ID, req)
	}}
}

// commands are the station commands this daemon understands. Anything else
// with an id is answered unsupported.
var commands = map[string]command{
	typeExecDispatch: handle(false, func(s *session, ctx context.Context, _ string, d protocol.ExecutionDispatch) (any, error) {
		return nil, s.client.handler.HandleExecutionDispatch(ctx, d)
	}),
	typeExecStop: handle(false, func(s *session, ctx context.Context, _ string, stop protocol.ExecutionStop) (any, error) {
		return nil, s.client.handler.HandleExecutionStop(ctx, stop)
	}),
	typeServiceApply: handle(false, func(s *session, _ context.Context, _ string, svc protocol.ServiceApply) (any, error) {
		defer s.rescanServices()
		return nil, s.client.handler.HandleServiceApply(svc)
	}),
	typeServiceControl: handle(false, func(s *session, _ context.Context, _ string, ctl protocol.ServiceControl) (any, error) {
		return nil, s.client.handler.HandleServiceControl(ctl)
	}),
	typeServiceRemove: handle(false, func(s *session, _ context.Context, _ string, rm protocol.ServiceRemove) (any, error) {
		defer s.rescanServices()
		return nil, s.client.handler.HandleServiceRemove(rm)
	}),
	typeLogSubscribe: handle(true, (*session).streamLog),
	typeLogSearch:    handle(true, (*session).searchLogs),
	typeAgentRestart: {run: func(_ context.Context, s *session, _ protocol.Envelope) (any, error) {
		return s.client.restart()
	}},
	typeAgentUpgrade: handle(true, func(s *session, ctx context.Context, _ string, u protocol.AgentUpgrade) (any, error) {
		return s.client.upgrade(ctx, u.Version)
	}),
	typeAgentCredentials: handle(false, func(s *session, ctx context.Context, _ string, c protocol.AgentCredentials) (any, error) {
		return nil, s.client.rotateToken(ctx, c.Token)
	}),
}
