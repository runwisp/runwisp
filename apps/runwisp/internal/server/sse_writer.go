// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// sseFrame is one SSE event ready for the wire. data must be JSON-serializable;
// id and event are optional. An empty id is omitted; an empty event uses the
// SSE default ("message").
type sseFrame struct {
	id    string
	event string
	data  any
}

// writeSSE serialises frame to w + flushes. The event/data lines follow the
// SSE spec: "id: <n>\nevent: <name>\ndata: <json>\n\n". Any embedded newlines
// in the JSON output are split across multiple data: lines so a chunked JSON
// payload still parses on the browser side.
func writeSSE(w http.ResponseWriter, flusher http.Flusher, frame sseFrame) error {
	var b strings.Builder
	if frame.id != "" {
		b.WriteString("id: ")
		b.WriteString(frame.id)
		b.WriteByte('\n')
	}
	if frame.event != "" {
		b.WriteString("event: ")
		b.WriteString(frame.event)
		b.WriteByte('\n')
	}
	payload, err := json.Marshal(frame.data)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(payload), "\n") {
		b.WriteString("data: ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	if _, err := fmt.Fprint(w, b.String()); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

// parseLastEventID extracts a non-negative line index from the SSE
// `Last-Event-ID` header. Returns ok=false when the header is empty or
// malformed.
func parseLastEventID(req *http.Request) (int64, bool) {
	raw := strings.TrimSpace(req.Header.Get("Last-Event-ID"))
	if raw == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}
