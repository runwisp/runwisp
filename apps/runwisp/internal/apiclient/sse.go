// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package apiclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// doSSE performs a GET request expecting an SSE stream.
// The returned response must be closed by the caller.
func (c *Client) doSSE(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create SSE request: %w", err)
	}

	req.Header.Set("Accept", "text/event-stream")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.streamClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("SSE connection failed: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		return nil, ErrUnauthorized
	}

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(errBody))}
	}

	return resp, nil
}
