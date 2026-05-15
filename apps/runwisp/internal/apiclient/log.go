// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package apiclient

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"

	"github.com/runwisp/runwisp/internal/server"
)

// GetLogPage fetches a JSON page of log lines. A negative `from` counts from
// the end (e.g. -1000 returns the tail). A positive `from` is interpreted as
// an absolute line anchor. limit <= 0 lets the server pick its default.
func (c *Client) GetLogPage(taskName, runID string, from, limit int64) (server.LogPageBody, error) {
	q := url.Values{}
	q.Set("from", fmt.Sprintf("%d", from))
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	path := fmt.Sprintf("/api/tasks/%s/runs/%s/log?%s", taskName, runID, q.Encode())
	resp, err := c.doRaw("GET", path)
	if err != nil {
		return server.LogPageBody{}, err
	}
	defer resp.Body.Close()

	var page server.LogPageBody
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return server.LogPageBody{}, fmt.Errorf("decode log page: %w", err)
	}
	return page, nil
}

// GetLogRaw streams the raw concatenated log file. The caller MUST close the
// returned reader. Use this for export / `cat` ergonomics — never as a
// streaming primitive.
func (c *Client) GetLogRaw(taskName, runID string) (io.ReadCloser, error) {
	path := fmt.Sprintf("/api/tasks/%s/runs/%s/log/raw", taskName, runID)
	resp, err := c.doRaw("GET", path)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}
