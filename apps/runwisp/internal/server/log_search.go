// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logsearch"
	"github.com/runwisp/runwisp/apps/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/apps/runwisp/internal/storage"
)

const (
	// LogSearchRunPageSize bounds the number of runs scanned per request,
	// so a task with thousands of historical runs paginates naturally
	// through the cursor rather than spending an unbounded scan budget on
	// one call.
	LogSearchRunPageSize = 50

	// LogSearchTimeout caps the wall time one search request may spend
	// scanning. Without it a low-match query keeps the scan goroutine and its
	// I/O busy long after the HTTP write timeout has already failed the client
	// response. Generous enough that a normal paginated page (bounded by
	// LogSearchRunPageSize / the hit budget) never trips it.
	LogSearchTimeout = 60 * time.Second
)

// LogSearchInput is the input to GET /api/tasks/{taskName}/log/search.
type LogSearchInput struct {
	TaskName      string `path:"taskName" minLength:"1" maxLength:"100" pattern:"^[a-zA-Z0-9._:-]+$" doc:"Task name"`
	RunID         string `query:"runId" doc:"Restrict the search to one run (ULID). Empty searches every non-deleted run of the task."`
	Q             string `query:"q" minLength:"1" maxLength:"1024" doc:"Substring (or regex when regex=true) to search for"`
	Regex         bool   `query:"regex" doc:"Treat q as an RE2 regular expression"`
	CaseSensitive bool   `query:"case" doc:"Match case-sensitively (default is case-insensitive)"`
	Limit         int    `query:"limit" minimum:"1" maximum:"1000" doc:"Max hits returned (default 200)"`
	Cursor        string `query:"cursor" doc:"Opaque continuation token returned by a previous call"`
}

// LogSearchHit is the wire shape for one match. Mirrors logsearch.Hit so the
// huma-generated OpenAPI schema does not need the internal package in its
// closure.
type LogSearchHit struct {
	RunID  string `json:"runId" doc:"ULID of the run containing this line"`
	N      int64  `json:"n" doc:"Absolute line number within the run"`
	Stream string `json:"stream" doc:"Stream identifier (stdout/stderr/system)"`
	Text   string `json:"text" doc:"Matched line content without trailing newline"`
	TS     int64  `json:"ts" doc:"Run created_at in Unix milliseconds (used for newest-first sort)"`
}

// LogSearchBody is the JSON response.
type LogSearchBody struct {
	Items       []LogSearchHit `json:"items" doc:"Hits ordered newest-run-first, then ascending line within a run"`
	NextCursor  string         `json:"nextCursor,omitempty" doc:"Opaque token to fetch the next page; empty when the scan is exhausted"`
	Exhausted   bool           `json:"exhausted" doc:"True when no further runs / lines need scanning"`
	ScannedRuns int            `json:"scannedRuns" doc:"Number of runs visited by this request"`
}

type LogSearchOutput struct {
	Body LogSearchBody
}

func (srv *Server) humaSearchLogs(ctx context.Context, input *LogSearchInput) (*LogSearchOutput, error) {
	ctx, cancel := context.WithTimeout(ctx, LogSearchTimeout)
	defer cancel()

	matcherFactory, err := buildMatcherFactory(input.Q, input.Regex, input.CaseSensitive)
	if err != nil {
		return nil, err
	}

	cursor, err := decodeSearchCursor(input.Cursor)
	if err != nil {
		return nil, huma.Error400BadRequest("Invalid cursor")
	}

	var boundary storage.LogSearchBoundary
	var resumeRunID string
	var firstN int64
	if cursor != nil {
		boundary = storage.LogSearchBoundary{CreationKey: cursor.CreationKey, ID: cursor.RunID, Include: !cursor.After}
		if !cursor.After {
			resumeRunID, firstN = cursor.RunID, cursor.NextN
		}
	}

	rows, err := srv.resolveSearchRuns(ctx, input, boundary)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return &LogSearchOutput{Body: LogSearchBody{Items: []LogSearchHit{}, Exhausted: true}}, nil
	}

	runs := make([]logsearch.RunRef, 0, len(rows))
	for _, row := range rows {
		runs = append(runs, logsearch.RunRef{
			ID: row.ID, CreatedAt: row.CreatedAt,
			LogPath: logutil.ResolveRunLogPath(srv.logDir, row.TaskName, row.ID, row.CreatedAt),
		})
	}

	hits, scanCursor, scanned, err := logsearch.ScanTask(ctx, runs, matcherFactory, logsearch.ScanOpts{MaxHits: input.Limit}, resumeRunID, firstN)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			// Client closed the connection mid-scan. huma will turn this
			// into a regular 408/500-class response after we return, but
			// the more useful signal is the lack of work: no log spam.
			return nil, huma.Error408RequestTimeout("Search cancelled")
		}
		slog.Error("Log search failed", "task", input.TaskName, "err", err)
		return nil, huma.Error500InternalServerError("Search failed")
	}

	wire := make([]LogSearchHit, len(hits))
	for i, h := range hits {
		wire[i] = LogSearchHit(h)
	}

	next := searchContinuation(rows, scanCursor, input.RunID == "")

	return &LogSearchOutput{Body: LogSearchBody{
		Items:       wire,
		NextCursor:  next,
		Exhausted:   next == "",
		ScannedRuns: scanned,
	}}, nil
}

func searchContinuation(rows []storage.LogSearchRun, scanCursor *logsearch.Cursor, taskWide bool) string {
	if scanCursor != nil {
		for _, row := range rows {
			if row.ID == scanCursor.RunID {
				return encodeSearchCursor(searchCursor{RunID: row.ID, CreationKey: row.CreationKey, NextN: scanCursor.NextN})
			}
		}
	}
	if taskWide && len(rows) == LogSearchRunPageSize {
		last := rows[len(rows)-1]
		return encodeSearchCursor(searchCursor{RunID: last.ID, CreationKey: last.CreationKey, After: true})
	}
	return ""
}

// resolveSearchRuns returns a newest-first keyset window, or one run
// belonging to the task when the request supplies a run ID.
func (srv *Server) resolveSearchRuns(ctx context.Context, input *LogSearchInput, boundary storage.LogSearchBoundary) ([]storage.LogSearchRun, error) {
	if input.RunID != "" {
		run, err := srv.getRunByID(ctx, input.RunID)
		if err != nil {
			if errors.Is(err, errInvalidRunID) {
				return nil, huma.Error400BadRequest("Invalid run ID")
			}
			return nil, mapDomainError(ctx, err, "Failed to load run")
		}
		// This search is scoped to the task in the URL, so a run_id belonging
		// to a different task is not found here.
		if run.TaskName != input.TaskName {
			return nil, huma.Error404NotFound("Run not found")
		}
		return []storage.LogSearchRun{{RunRef: storage.RunRef{
			ID: run.ID, TaskName: run.TaskName, CreatedAt: run.CreatedAt,
		}}}, nil
	}

	runs, err := srv.db.ListLogSearchRuns(ctx, input.TaskName, LogSearchRunPageSize, boundary)
	if err != nil {
		return nil, mapDomainError(ctx, err, "Failed to list runs")
	}
	return runs, nil
}

// buildMatcherFactory returns a function that produces a fresh matcher per
// scan goroutine. Per-line state (the case-fold scratch buffer for the
// substring matcher) cannot be shared across goroutines.
func buildMatcherFactory(q string, regex, caseSensitive bool) (func() logsearch.Matcher, error) {
	// Pre-validate once so a malformed regex maps to 400 before any worker
	// starts. The factory then compiles a fresh matcher per goroutine.
	if _, err := logsearch.NewMatcher(q, regex, caseSensitive); err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	return func() logsearch.Matcher {
		m, _ := logsearch.NewMatcher(q, regex, caseSensitive)
		return m
	}, nil
}

// searchCursor names either the next run and line to scan, or the last
// run of a fully scanned window (After).
type searchCursor struct {
	CreationKey string `json:"c,omitempty"`
	RunID       string `json:"r"`
	NextN       int64  `json:"n,omitempty"`
	After       bool   `json:"a,omitempty"`
}

// encodeSearchCursor / decodeSearchCursor round-trip the cursor as
// base64-JSON. The cursor is opaque to the client and only needs to be
// stable across one server build, so JSON is the cheapest correct format.
func encodeSearchCursor(c searchCursor) string {
	data, _ := json.Marshal(c)
	return base64.URLEncoding.EncodeToString(data)
}

func decodeSearchCursor(raw string) (*searchCursor, error) {
	if raw == "" {
		return nil, nil
	}
	data, err := base64.URLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	var c searchCursor
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.RunID == "" || c.NextN < 0 {
		return nil, errors.New("invalid search position")
	}
	return &c, nil
}
