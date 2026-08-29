// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/sse"
)

func (srv *Server) registerDaemonLogSSE(api huma.API) {
	sse.Register(api, huma.Operation{
		OperationID: "streamDaemonLog",
		Method:      http.MethodGet,
		Path:        "/api/daemon/log/stream",
		Summary:     "Stream the daemon's recent log output",
		Description: "Server-Sent Events stream of daemon log lines. Replays the last 100 buffered lines, then emits new lines as they're written until the client disconnects.",
		Tags:        []string{"System"},
	}, map[string]any{
		"line": DaemonLogLineEvent{},
	}, srv.sseDaemonLogHandler)
}

func (srv *Server) sseDaemonLogHandler(ctx context.Context, _ *struct{}, send sse.Sender) {
	release, ok := srv.streams.acquire(ctx)
	if !ok {
		return
	}
	defer release()

	if srv.daemonLogBuffer == nil {
		return
	}

	subID, backlog, ch := srv.daemonLogBuffer.SubscribeWithBacklog(100)
	defer srv.daemonLogBuffer.Unsubscribe(subID)

	for _, line := range backlog {
		if err := send(sse.Message{Data: DaemonLogLineEvent{Line: line}}); err != nil {
			return
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-ch:
			if !ok {
				return
			}
			if err := send(sse.Message{Data: DaemonLogLineEvent{Line: line}}); err != nil {
				return
			}
		}
	}
}
