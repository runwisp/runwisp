// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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
	resp, err := c.doRaw(path)
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
	resp, err := c.doRaw(path)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// GetLogLineHistory fetches the prior whole-region frames a settled progress
// bar or multi-line redraw passed through before committing line n. Returns an
// empty slice when the line has no recorded history.
func (c *Client) GetLogLineHistory(taskName, runID string, n int64) ([][]string, error) {
	path := fmt.Sprintf("/api/tasks/%s/runs/%s/log/line/%d/history", taskName, runID, n)
	resp, err := c.doRaw(path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var body server.LogLineHistoryBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode log line history: %w", err)
	}
	return body.Frames, nil
}

// SearchLogsOptions parameterises a SearchLogs call. Zero values pick the
// server defaults: substring match, case-insensitive, 200-hit page.
type SearchLogsOptions struct {
	Query         string
	Regex         bool
	CaseSensitive bool
	RunID         string // optional — empty means search every run of the task
	Limit         int    // 0 = server default
	Cursor        string // opaque continuation token from a previous response
}

// SearchLogs runs an on-demand log search and returns the parsed body. The
// returned cursor is empty when the scan reached the end of available runs.
func (c *Client) SearchLogs(taskName string, opts SearchLogsOptions) (server.LogSearchBody, error) {
	q := url.Values{}
	q.Set("q", opts.Query)
	if opts.Regex {
		q.Set("regex", "true")
	}
	if opts.CaseSensitive {
		q.Set("case", "true")
	}
	if opts.RunID != "" {
		q.Set("runId", opts.RunID)
	}
	if opts.Limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", opts.Limit))
	}
	if opts.Cursor != "" {
		q.Set("cursor", opts.Cursor)
	}
	path := fmt.Sprintf("/api/tasks/%s/log/search?%s", taskName, q.Encode())
	resp, err := c.doRaw(path)
	if err != nil {
		return server.LogSearchBody{}, err
	}
	defer resp.Body.Close()

	var body server.LogSearchBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return server.LogSearchBody{}, fmt.Errorf("decode log search: %w", err)
	}
	return body, nil
}
