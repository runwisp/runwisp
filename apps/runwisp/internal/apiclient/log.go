// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package apiclient

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
)

// LogLine matches the SSE/line-page entry shape published by the daemon.
type LogLine struct {
	N         int64  `json:"n"`
	Ts        int64  `json:"ts"`
	Stream    string `json:"stream"`
	Text      string `json:"text"`
	Continued bool   `json:"continued,omitempty"`
}

// LogPage is the JSON page returned by GET /log.
type LogPage struct {
	Lines          []LogLine `json:"lines"`
	FirstAvailable int64     `json:"first_available"`
	TotalLines     int64     `json:"total_lines"`
	Truncated      bool      `json:"truncated"`
	Finalized      bool      `json:"finalized"`
}

// GetLogPage fetches a JSON page of log lines. A negative `from` counts from
// the end (e.g. -1000 returns the tail). A positive `from` is interpreted as
// an absolute line anchor. limit <= 0 lets the server pick its default.
func (c *Client) GetLogPage(taskName, runID string, from, limit int64) (LogPage, error) {
	q := url.Values{}
	q.Set("from", fmt.Sprintf("%d", from))
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	path := fmt.Sprintf("/api/tasks/%s/runs/%s/log?%s", taskName, runID, q.Encode())
	resp, err := c.doRaw("GET", path)
	if err != nil {
		return LogPage{}, err
	}
	defer resp.Body.Close()

	var page LogPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return LogPage{}, fmt.Errorf("decode log page: %w", err)
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
