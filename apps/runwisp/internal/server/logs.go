// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"log/slog"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/sse"
	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"github.com/runwisp/runwisp/internal/server/logstream"
)

const (
	// LogStreamTimeout caps a single SSE connection's lifetime. The TUI/UI
	// reconnects automatically, so this isn't user-visible.
	LogStreamTimeout = 10 * time.Minute

	// LogPageDefaultLimit is the default `limit` for /log JSON pages.
	LogPageDefaultLimit = 1000
	// LogPageMaxLimit caps the JSON page size to prevent unbounded memory.
	LogPageMaxLimit = 10000
	// The default anchor when no `from` is given (a tail of the last 1000
	// lines) is expressed as the `default:"-1000"` struct tag on the From
	// query field, so huma can distinguish an absent param from an explicit
	// from=0 (the first line). Don't reintroduce a `from == 0` fallback here:
	// that conflates "first line" with "unset" and hangs scroll-to-top.

	// LogStreamReplayDefault is the default replay window for /log/stream.
	LogStreamReplayDefault = 5000
	// LogStreamReplayMax caps the replay window.
	LogStreamReplayMax = 50000
)

// LogPageInput drives GET /api/tasks/{taskName}/runs/{runId}/log.
type LogPageInput struct {
	TaskName string `path:"taskName" minLength:"1" maxLength:"100" pattern:"^[a-zA-Z0-9._-]+$" doc:"Task name"`
	RunID    string `path:"runId" minLength:"26" maxLength:"26" pattern:"^[0-9A-HJKMNP-TV-Z]{26}$" doc:"Run ULID"`
	From     int64  `query:"from" default:"-1000" doc:"Anchor line number; 0 is the first line, negative values count from end (default -1000)"`
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
func (srv *Server) resolveLogPathFor(ctx context.Context, taskName, runIDStr string) (string, *model.Run, error) {
	if _, err := ulid.Parse(runIDStr); err != nil {
		return "", nil, huma.Error400BadRequest("Invalid run ID")
	}
	run, err := srv.db.GetRun(ctx, runIDStr)
	if err != nil {
		return "", nil, huma.Error404NotFound("Run not found")
	}
	if run.TaskName != taskName {
		return "", nil, huma.Error404NotFound("Run not found")
	}
	return logutil.ResolveRunLogPath(srv.logDir, run.TaskName, run.ID, run.CreatedAt), run, nil
}

func (srv *Server) humaGetLogPage(ctx context.Context, input *LogPageInput) (*LogPageOutput, error) {
	logPath, run, err := srv.resolveLogPathFor(ctx, input.TaskName, input.RunID)
	if err != nil {
		return nil, err
	}

	from := input.From
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
	logPath, _, err := srv.resolveLogPathFor(ctx, input.TaskName, input.RunID)
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

// LogStreamInput drives the SSE log endpoint. Huma performs path/query/header
// validation against these tags so the handler only sees vetted values.
type LogStreamInput struct {
	TaskName    string `path:"taskName" minLength:"1" maxLength:"100" pattern:"^[a-zA-Z0-9._-]+$" doc:"Task name"`
	RunID       string `path:"runId" minLength:"26" maxLength:"26" pattern:"^[0-9A-HJKMNP-TV-Z]{26}$" doc:"Run ULID"`
	From        int64  `query:"from" default:"-1000" doc:"Anchor line number; 0 is the first line, negative values count from end (default -1000)"`
	ReplayLimit int64  `query:"replay_limit" minimum:"1" maximum:"50000" doc:"Cap on backfilled lines (default 5000)"`
	LastEventID string `header:"Last-Event-ID" doc:"Native SSE resume cursor; takes precedence over the from query"`
}

func (srv *Server) registerLogSSE(api huma.API) {
	sse.Register(api, huma.Operation{
		OperationID: "streamLog",
		Method:      http.MethodGet,
		Path:        "/api/tasks/{taskName}/runs/{runId}/log/stream",
		Summary:     "Stream a run's log lines as SSE",
		Description: "Server-Sent Events stream of absolute-line-numbered log entries. Replays history starting at `from` (or `Last-Event-ID + 1`), then follows live output until the run terminates.",
		Tags:        []string{"Logs"},
	}, map[string]any{
		"line":    LogLineSSEEvent{},
		"rotated": LogRotatedEvent{},
		"dropped": LogDroppedEvent{},
		"done":    LogDoneEvent{},
	}, func(ctx context.Context, input *LogStreamInput, send sse.Sender) {
		release, ok := srv.streams.acquire(streamClientIPFromCtx(ctx))
		if !ok {
			return
		}
		defer release()

		if _, err := ulid.Parse(input.RunID); err != nil {
			return
		}
		run, err := srv.db.GetRun(ctx, input.RunID)
		if err != nil {
			slog.Error("Failed to get run", "run", input.RunID, "err", err)
			return
		}
		if run.TaskName != input.TaskName {
			return
		}

		logPath := logutil.ResolveRunLogPath(srv.logDir, run.TaskName, run.ID, run.CreatedAt)

		from := input.From
		if resume, ok := parseResumeID(input.LastEventID); ok {
			from = resume + 1
		}

		replay := input.ReplayLimit
		if replay <= 0 {
			replay = LogStreamReplayDefault
		}

		streamCtx, cancel := context.WithTimeout(ctx, LogStreamTimeout)
		defer cancel()

		sender := logstream.HumaSender{
			Inner:   send,
			Line:    func(e logstream.LineEvent) any { return LogLineSSEEvent(e) },
			Rotated: func(e logstream.RotatedEvent) any { return LogRotatedEvent(e) },
			Dropped: func(e logstream.DroppedEvent) any { return LogDroppedEvent(e) },
			Done:    func(e logstream.DoneEvent) any { return LogDoneEvent(e) },
		}
		logstream.Run(streamCtx, logstream.StreamConfig{
			Send:        sender,
			LogPath:     logPath,
			RunID:       run.ID,
			Bus:         srv.eventBus,
			DB:          srv.db,
			AnchorFrom:  from,
			ReplayLimit: replay,
			RunEnded:    run.Status.IsTerminal(),
		})
	})
}

// parseResumeID extracts a non-negative line index from the SSE
// `Last-Event-ID` header. Returns ok=false when the header is empty or
// malformed.
func parseResumeID(raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}
