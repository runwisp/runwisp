// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
)

func (srv *Server) handleDaemonLogStream(resp http.ResponseWriter, req *http.Request) {
	if srv.daemonLogBuffer == nil {
		respondNotFound(resp, "Daemon log buffer not available")
		return
	}

	resp.Header().Set("Content-Type", "text/event-stream")
	resp.Header().Set("Cache-Control", "no-cache")
	resp.Header().Set("Connection", "keep-alive")
	resp.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := resp.(http.Flusher)
	if !ok {
		respondError(resp, http.StatusInternalServerError, "Streaming unsupported", nil)
		return
	}

	// Backfill recent lines.
	for _, line := range srv.daemonLogBuffer.Lines(100) {
		data, _ := json.Marshal(line)
		fmt.Fprintf(resp, "data: %s\n\n", data)
	}
	flusher.Flush()

	// Subscribe to new lines.
	subID, ch := srv.daemonLogBuffer.Subscribe()
	defer srv.daemonLogBuffer.Unsubscribe(subID)

	ctx := req.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-ch:
			if !ok {
				return
			}
			data, _ := json.Marshal(line)
			fmt.Fprintf(resp, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}
