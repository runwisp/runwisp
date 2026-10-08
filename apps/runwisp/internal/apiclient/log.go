// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package apiclient

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/runwisp/runwisp/internal/server"
)

// GetLogPage fetches a JSON page of log lines. A negative `from` counts from
// the end (e.g. -1000 returns the tail). A positive `from` is interpreted as
// an absolute line anchor. limit <= 0 lets the server pick its default.
func (c *Client) GetLogPage(ctx context.Context, runID string, from, limit int64) (server.LogPageBody, error) {
	q := url.Values{}
	q.Set("from", strconv.FormatInt(from, 10))
	if limit > 0 {
		q.Set("limit", strconv.FormatInt(limit, 10))
	}
	var page server.LogPageBody
	err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/api/runs/%s/log?%s", runID, q.Encode()), nil, &page)
	return page, err
}

// GetLogRaw streams the raw concatenated log file. The caller MUST close the
// returned reader. Use this for export / `cat` ergonomics — never as a
// streaming primitive.
func (c *Client) GetLogRaw(ctx context.Context, runID string) (io.ReadCloser, error) {
	path := fmt.Sprintf("/api/runs/%s/log/raw", runID)
	resp, err := c.doRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// GetLogLineHistory fetches the prior whole-region frames a settled progress
// bar or multi-line redraw passed through before committing line n. Returns an
// empty slice when the line has no recorded history.
func (c *Client) GetLogLineHistory(ctx context.Context, runID string, n int64) ([][]string, error) {
	path := fmt.Sprintf("/api/runs/%s/log/line/%d/history", runID, n)
	body, err := doJSONAs[server.LogLineHistoryBody](ctx, c, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
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
func (c *Client) SearchLogs(ctx context.Context, taskName string, opts SearchLogsOptions) (server.LogSearchBody, error) {
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
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.Cursor != "" {
		q.Set("cursor", opts.Cursor)
	}
	var body server.LogSearchBody
	err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/api/tasks/%s/log/search?%s", taskName, q.Encode()), nil, &body)
	return body, err
}
