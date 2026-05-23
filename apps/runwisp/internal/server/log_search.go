// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"

	"log/slog"

	"github.com/danielgtaylor/huma/v2"
	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/logsearch"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/storage"
)

const (
	// LogSearchDefaultLimit is the default number of hits returned by the
	// search endpoint when the client omits the `limit` query parameter.
	LogSearchDefaultLimit = 200

	// LogSearchMaxLimit caps the per-request hit budget; the value matches
	// logsearch.MaxHitsCeiling so a clamped huma value still allows a full
	// library page.
	LogSearchMaxLimit = 1000

	// LogSearchRunPageSize bounds the number of runs scanned per request,
	// so a task with thousands of historical runs paginates naturally
	// through the cursor rather than spending an unbounded scan budget on
	// one call.
	LogSearchRunPageSize = 50
)

// LogSearchInput is the input to GET /api/tasks/{taskName}/log/search.
type LogSearchInput struct {
	TaskName      string `path:"taskName" minLength:"1" maxLength:"100" pattern:"^[a-zA-Z0-9._-]+$" doc:"Task name"`
	RunID         string `query:"run_id" doc:"Restrict the search to one run (ULID). Empty searches every non-deleted run of the task."`
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
	RunID  string `json:"run_id" doc:"ULID of the run containing this line"`
	N      int64  `json:"n" doc:"Absolute line number within the run"`
	Stream string `json:"stream" doc:"Stream identifier (stdout/stderr/system)"`
	Text   string `json:"text" doc:"Matched line content without trailing newline"`
	TS     int64  `json:"ts" doc:"Run created_at in Unix milliseconds (used for newest-first sort)"`
}

// LogSearchBody is the JSON response.
type LogSearchBody struct {
	Hits        []LogSearchHit `json:"hits" doc:"Hits ordered newest-run-first, then ascending line within a run"`
	NextCursor  string         `json:"next_cursor,omitempty" doc:"Opaque token to fetch the next page; empty when the scan is exhausted"`
	Exhausted   bool           `json:"exhausted" doc:"True when no further runs / lines need scanning"`
	ScannedRuns int            `json:"scanned_runs" doc:"Number of runs visited by this request"`
}

type LogSearchOutput struct {
	Body LogSearchBody
}

func (srv *Server) humaSearchLogs(ctx context.Context, input *LogSearchInput) (*LogSearchOutput, error) {
	matcherFactory, err := buildMatcherFactory(input.Q, input.Regex, input.CaseSensitive)
	if err != nil {
		return nil, err
	}

	limit := input.Limit
	if limit <= 0 {
		limit = LogSearchDefaultLimit
	}
	if limit > LogSearchMaxLimit {
		limit = LogSearchMaxLimit
	}

	cursor, err := decodeSearchCursor(input.Cursor)
	if err != nil {
		return nil, huma.Error400BadRequest("Invalid cursor")
	}

	runs, err := srv.resolveSearchRuns(ctx, input)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return &LogSearchOutput{Body: LogSearchBody{Hits: []LogSearchHit{}, Exhausted: true}}, nil
	}

	startAfterRunID := ""
	var startAfterN int64
	if cursor != nil {
		startAfterRunID = cursor.RunID
		startAfterN = cursor.NextN
	}

	hits, nextCursor, scanned, err := logsearch.ScanTask(ctx, runs, matcherFactory, logsearch.ScanOpts{MaxHits: limit}, startAfterRunID, startAfterN)
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

	exhausted := nextCursor == nil && len(runs) < LogSearchRunPageSize
	encoded := ""
	if nextCursor != nil {
		encoded = encodeSearchCursor(*nextCursor)
	}

	return &LogSearchOutput{Body: LogSearchBody{
		Hits:        wire,
		NextCursor:  encoded,
		Exhausted:   exhausted,
		ScannedRuns: scanned,
	}}, nil
}

// resolveSearchRuns returns the runs to scan in newest-first order. When
// run_id is supplied, the slice has at most one element (filtered to ensure
// the run belongs to the task — same 404 contract as the per-run log
// endpoints). Otherwise it lists up to LogSearchRunPageSize newest runs.
func (srv *Server) resolveSearchRuns(ctx context.Context, input *LogSearchInput) ([]logsearch.RunRef, error) {
	if input.RunID != "" {
		if _, err := ulid.Parse(input.RunID); err != nil {
			return nil, huma.Error400BadRequest("Invalid run ID")
		}
		run, err := srv.db.GetRun(ctx, input.RunID)
		if err != nil {
			return nil, huma.Error404NotFound("Run not found")
		}
		if run.TaskName != input.TaskName {
			return nil, huma.Error404NotFound("Run not found")
		}
		return []logsearch.RunRef{{
			ID:        run.ID,
			LogPath:   logutil.ResolveRunLogPath(srv.logDir, run.TaskName, run.ID, run.CreatedAt),
			CreatedAt: run.CreatedAt,
		}}, nil
	}

	runs, err := srv.db.QueryRuns(ctx, input.TaskName, LogSearchRunPageSize, 0, "", storage.SortColumnCreatedAt, storage.SortDesc, "")
	if err != nil {
		return nil, huma.Error500InternalServerError("Failed to list runs")
	}
	out := make([]logsearch.RunRef, 0, len(runs))
	for _, r := range runs {
		out = append(out, logsearch.RunRef{
			ID:        r.ID,
			LogPath:   logutil.ResolveRunLogPath(srv.logDir, r.TaskName, r.ID, r.CreatedAt),
			CreatedAt: r.CreatedAt,
		})
	}
	return out, nil
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

// encodeSearchCursor / decodeSearchCursor round-trip the cursor as
// base64-JSON. The cursor is opaque to the client and only needs to be
// stable across one server build, so JSON is the cheapest correct format.
func encodeSearchCursor(c logsearch.Cursor) string {
	data, _ := json.Marshal(c)
	return base64.URLEncoding.EncodeToString(data)
}

func decodeSearchCursor(raw string) (*logsearch.Cursor, error) {
	if raw == "" {
		return nil, nil
	}
	data, err := base64.URLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	var c logsearch.Cursor
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	return &c, nil
}
