// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package apiclient

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	ssePrefixEvent = "event: "
	ssePrefixData  = "data: "
	ssePrefixID    = "id: "
)

// doSSE performs a GET request expecting an SSE stream.
// The returned response must be closed by the caller.
func (c *Client) doSSE(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create SSE request: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	return c.send(c.streamClient, req)
}

// sseFrame is one complete SSE event: the lines up to a blank line, with
// multi-line data joined by "\n".
type sseFrame struct {
	event, id, data string
}

// readSSEFrames reads SSE frames from body and hands each one carrying data to
// emit until the body ends or emit returns false. id:/event:/data: lines are
// folded into the frame; retry:, comments and unknown lines are ignored. It
// returns the read error, if any.
func readSSEFrames(body io.Reader, emit func(sseFrame) bool) error {
	scanner := bufio.NewScanner(body)
	// A backfill burst can carry up to a 64 KiB log line plus JSON overhead;
	// 256 KiB is a comfortable headroom.
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	var frame sseFrame
	var data []string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case line == "":
			if len(data) > 0 {
				frame.data = strings.Join(data, "\n")
				if !emit(frame) {
					return nil
				}
			}
			frame, data = sseFrame{}, data[:0]
		case strings.HasPrefix(line, ssePrefixID):
			frame.id = strings.TrimPrefix(line, ssePrefixID)
		case strings.HasPrefix(line, ssePrefixEvent):
			frame.event = strings.TrimPrefix(line, ssePrefixEvent)
		case strings.HasPrefix(line, ssePrefixData):
			data = append(data, strings.TrimPrefix(line, ssePrefixData))
		}
	}
	return scanner.Err()
}
