// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"log/slog"
)

const (
	// LogStreamTimeout caps a single SSE connection's lifetime. The TUI/UI
	// reconnects automatically, so this isn't user-visible.
	LogStreamTimeout = 10 * time.Minute

	// LogPageDefaultLimit is the default `limit` for /log JSON pages.
	LogPageDefaultLimit = 1000
	// LogPageMaxLimit caps the JSON page size to prevent unbounded memory.
	LogPageMaxLimit = 10000
	// LogPageDefaultFrom is the default tail size when no `from` is given.
	LogPageDefaultFrom = -1000

	// LogStreamReplayDefault is the default replay window for /log/stream.
	LogStreamReplayDefault = 5000
	// LogStreamReplayMax caps the replay window.
	LogStreamReplayMax = 50000
)

// LogPageInput drives GET /api/tasks/{taskName}/runs/{runId}/log.
type LogPageInput struct {
	TaskName string `path:"taskName" minLength:"1" maxLength:"100" pattern:"^[a-zA-Z0-9._-]+$" doc:"Task name"`
	RunID    string `path:"runId" minLength:"26" maxLength:"26" pattern:"^[0-9A-HJKMNP-TV-Z]{26}$" doc:"Run ULID"`
	From     int64  `query:"from" doc:"Anchor line number; negative values count from end (default -1000)"`
	Limit    int64  `query:"limit" minimum:"1" maximum:"10000" doc:"Max lines returned (default 1000)"`
}

// LogLineEntry mirrors the line-event payload used on the SSE wire.
type LogLineEntry struct {
	N         int64  `json:"n" doc:"Absolute line number"`
	Ts        int64  `json:"ts" doc:"Unix milliseconds timestamp; 0 if unavailable"`
	Stream    string `json:"stream" doc:"Stream identifier (stdout/stderr/system)"`
	Text      string `json:"text" doc:"Line content without trailing newline"`
	Continued bool   `json:"continued,omitempty" doc:"True if this segment continues an oversized split line"`
}

// LogPageBody is the response shape for the line-page endpoint.
type LogPageBody struct {
	Lines          []LogLineEntry `json:"lines" doc:"Returned lines, ascending by n"`
	FirstAvailable int64          `json:"first_available" doc:"Lowest line number still on disk; lines below were rotated away"`
	TotalLines     int64          `json:"total_lines" doc:"Total lines produced across all segments"`
	Truncated      bool           `json:"truncated" doc:"True if rotation has dropped lines below first_available"`
	Finalized      bool           `json:"finalized" doc:"True if the run has ended and the log is final"`
}

type LogPageOutput struct {
	Body LogPageBody
}

// LogRawInput drives GET /api/tasks/{taskName}/runs/{runId}/log/raw.
type LogRawInput struct {
	TaskName string `path:"taskName" minLength:"1" maxLength:"100" pattern:"^[a-zA-Z0-9._-]+$" doc:"Task name"`
	RunID    string `path:"runId" minLength:"26" maxLength:"26" pattern:"^[0-9A-HJKMNP-TV-Z]{26}$" doc:"Run ULID"`
}

// LogRawOutput streams the rotated-away segment (.log.prev) followed by the
// current segment, concatenated into one text/plain body.
type LogRawOutput struct {
	ContentType string `header:"Content-Type"`
	Body        []byte
}

// resolveLogPath validates the run ID and computes the log path. Returns an
// error suitable for huma to map to an HTTP status code.
func (srv *Server) resolveLogPathFor(taskName, runIDStr string) (string, *model.Run, error) {
	if _, err := ulid.Parse(runIDStr); err != nil {
		return "", nil, huma.Error400BadRequest("Invalid run ID")
	}
	run, err := srv.db.GetRun(runIDStr)
	if err != nil {
		return "", nil, huma.Error404NotFound("Run not found")
	}
	if run.TaskName != taskName {
		return "", nil, huma.Error404NotFound("Run not found")
	}
	return logutil.ResolveRunLogPath(srv.logDir, run.TaskName, run.ID, run.CreatedAt), run, nil
}

func (srv *Server) humaGetLogPage(ctx context.Context, input *LogPageInput) (*LogPageOutput, error) {
	logPath, run, err := srv.resolveLogPathFor(input.TaskName, input.RunID)
	if err != nil {
		return nil, err
	}

	from := input.From
	if from == 0 {
		from = LogPageDefaultFrom
	}
	limit := input.Limit
	if limit <= 0 {
		limit = LogPageDefaultLimit
	}
	if limit > LogPageMaxLimit {
		limit = LogPageMaxLimit
	}

	lines, firstAvailable, totalLines, readErr := logutil.ReadLineRange(logPath, from, limit)
	if readErr != nil {
		slog.Error("Failed to read log line range", "path", logPath, "err", readErr)
		return nil, huma.Error500InternalServerError("Failed to read log")
	}

	entries := make([]LogLineEntry, len(lines))
	for i, l := range lines {
		entries[i] = LogLineEntry{
			N:      l.LineNum,
			Stream: l.Stream,
			Text:   l.Text,
		}
	}

	return &LogPageOutput{Body: LogPageBody{
		Lines:          entries,
		FirstAvailable: firstAvailable,
		TotalLines:     totalLines,
		Truncated:      firstAvailable > 0,
		Finalized:      run.Status.IsTerminal(),
	}}, nil
}

func (srv *Server) humaGetLogRaw(ctx context.Context, input *LogRawInput) (*LogRawOutput, error) {
	logPath, _, err := srv.resolveLogPathFor(input.TaskName, input.RunID)
	if err != nil {
		return nil, err
	}

	body, readErr := readRawLog(logPath)
	if readErr != nil {
		slog.Error("Failed to read raw log", "path", logPath, "err", readErr)
		return nil, huma.Error500InternalServerError("Failed to read log")
	}

	return &LogRawOutput{
		ContentType: "text/plain; charset=utf-8",
		Body:        body,
	}, nil
}

// readRawLog concatenates the rotated-away segment (if any) and the current
// segment so a single download contains the operator-visible byte stream.
func readRawLog(logPath string) ([]byte, error) {
	var body []byte
	if prev, err := os.ReadFile(logPath + ".prev"); err == nil {
		body = append(body, prev...)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if cur, err := os.ReadFile(logPath); err == nil {
		body = append(body, cur...)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return body, nil
}

// handleLogStream is the raw-chi SSE endpoint. Huma's SSE support requires a
// fixed event-name → struct map, but we want JSON-serialised line events with
// `id:` set per line so EventSource's native Last-Event-ID resume works.
func (srv *Server) handleLogStream(resp http.ResponseWriter, req *http.Request) {
	taskName := chi.URLParam(req, "taskName")
	runIDStr := chi.URLParam(req, "runId")
	if _, err := ulid.Parse(runIDStr); err != nil {
		respondBadRequest(resp, "Invalid run ID")
		return
	}

	run, err := srv.db.GetRun(runIDStr)
	if err != nil {
		slog.Error("Failed to get run", "run", runIDStr, "err", err)
		respondNotFound(resp, "Run not found")
		return
	}
	if run.TaskName != taskName {
		respondNotFound(resp, "Run not found")
		return
	}

	rc := http.NewResponseController(resp)
	if err := rc.SetWriteDeadline(time.Now().Add(LogStreamTimeout + 30*time.Second)); err != nil {
		slog.Warn("Failed to extend write deadline for SSE", "err", err)
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

	ctx, cancel := context.WithTimeout(req.Context(), LogStreamTimeout)
	defer cancel()

	logPath := logutil.ResolveRunLogPath(srv.logDir, run.TaskName, run.ID, run.CreatedAt)

	from := parseFromQuery(req.URL.Query().Get("from"))
	if resumeFrom, ok := parseLastEventID(req); ok {
		from = resumeFrom + 1
	}
	replayLimit := parseReplayLimitQuery(req.URL.Query().Get("replay_limit"))

	streamer := newLogStreamer(resp, flusher, logPath)
	streamer.streamLoop(ctx, run.ID, srv.eventBus, srv.db, from, replayLimit, run.Status.IsTerminal())
}

// parseFromQuery interprets the SSE `from` query the same as the JSON page:
// missing → tail-from-end default; explicit value → exact line anchor.
func parseFromQuery(raw string) int64 {
	if raw == "" {
		return LogPageDefaultFrom
	}
	v, err := parseInt64(raw)
	if err != nil {
		return LogPageDefaultFrom
	}
	return v
}

func parseReplayLimitQuery(raw string) int64 {
	if raw == "" {
		return LogStreamReplayDefault
	}
	v, err := parseInt64(raw)
	if err != nil || v <= 0 {
		return LogStreamReplayDefault
	}
	if v > LogStreamReplayMax {
		v = LogStreamReplayMax
	}
	return v
}

func parseInt64(raw string) (int64, error) {
	var sign int64 = 1
	if len(raw) > 0 && (raw[0] == '-' || raw[0] == '+') {
		if raw[0] == '-' {
			sign = -1
		}
		raw = raw[1:]
	}
	if raw == "" {
		return 0, errors.New("empty integer")
	}
	var n int64
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, errors.New("non-digit in integer")
		}
		n = n*10 + int64(c-'0')
	}
	return n * sign, nil
}
