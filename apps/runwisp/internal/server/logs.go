// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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
	// The default tail anchor lives in the From field's `default:"-1000"` struct
	// tag so huma can tell an absent param from an explicit from=0 (first line).
	// Don't reintroduce a `from == 0` fallback here: it conflates "first line"
	// with "unset" and hangs scroll-to-top.

	// LogStreamReplayDefault is the default replay window for /log/stream.
	LogStreamReplayDefault = 5000
	LogStreamReplayMax     = 50000
)

// LogPageInput drives GET /api/runs/{runId}/log.
type LogPageInput struct {
	RunID string `path:"runId" minLength:"26" maxLength:"26" pattern:"^[0-9A-HJKMNP-TV-Z]{26}$" doc:"Run ULID"`
	From  int64  `query:"from" default:"-1000" doc:"Anchor line number; 0 is the first line, negative values count from end (default -1000)"`
	Limit int64  `query:"limit" minimum:"1" maximum:"10000" doc:"Max lines returned (default 1000)"`
}

// LogLineEntry mirrors the line-event payload used on the SSE wire.
type LogLineEntry struct {
	N          int64  `json:"n" doc:"Absolute line number"`
	Ts         int64  `json:"ts" doc:"Unix milliseconds timestamp; 0 if unavailable"`
	Stream     string `json:"stream" doc:"Stream identifier (stdout/stderr/system)"`
	Text       string `json:"text" doc:"Line content without trailing newline"`
	Continued  bool   `json:"continued,omitempty" doc:"True if this segment continues an oversized split line"`
	FrameCount int    `json:"frameCount,omitempty" doc:"Number of recorded prior frames if this line is a settled progress bar / redraw anchor; 0 otherwise"`
}

// LogPageBody is the response shape for the line-page endpoint.
type LogPageBody struct {
	Lines          []LogLineEntry `json:"lines" doc:"Returned lines, ascending by n"`
	FirstAvailable int64          `json:"firstAvailable" doc:"Lowest line number still on disk; lines below were rotated away"`
	TotalLines     int64          `json:"totalLines" doc:"Total lines produced across all segments"`
	Truncated      bool           `json:"truncated" doc:"True if rotation has dropped lines below first_available"`
	Finalized      bool           `json:"finalized" doc:"True if the run has ended and the log is final"`
}

type LogPageOutput struct {
	Body LogPageBody
}

// LogRawInput drives GET /api/runs/{runId}/log/raw.
type LogRawInput struct {
	RunID string `path:"runId" minLength:"26" maxLength:"26" pattern:"^[0-9A-HJKMNP-TV-Z]{26}$" doc:"Run ULID"`
}

// LogRawOutput streams the rotated-away segment (.log.prev) followed by the
// current segment, concatenated into one text/plain body.
type LogRawOutput struct {
	ContentType string `header:"Content-Type"`
	Body        []byte
}

// errInvalidRunID is getRunByID's sentinel for a malformed ULID, distinguished
// from a plain lookup failure (whose underlying error passes through unwrapped)
// so each caller can map to its own response shape.
var errInvalidRunID = errors.New("invalid run id")

// getRunByID parses runIDStr and loads the run. A run ULID is globally unique,
// so a run is addressed by ID alone — no task name is needed. Callers translate
// the returned sentinel/wrapped errors into their own response (a 400/404 pair
// for REST, a silent drop for SSE).
func (srv *Server) getRunByID(ctx context.Context, runIDStr string) (*model.Run, error) {
	if _, err := ulid.Parse(runIDStr); err != nil {
		return nil, errInvalidRunID
	}
	run, err := srv.db.GetRun(ctx, runIDStr)
	if err != nil {
		return nil, err
	}
	return run, nil
}

// resolveLogPath validates the run ID and computes the log path. Returns
// an error suitable for huma to map to an HTTP status code.
func (srv *Server) resolveLogPath(ctx context.Context, runIDStr string) (string, *model.Run, error) {
	run, err := srv.getRunByID(ctx, runIDStr)
	if err != nil {
		if errors.Is(err, errInvalidRunID) {
			return "", nil, huma.Error400BadRequest("Invalid run ID")
		}
		return "", nil, huma.Error404NotFound("Run not found")
	}
	return logutil.ResolveRunLogPath(srv.logDir, run.TaskName, run.ID, run.CreatedAt), run, nil
}

func (srv *Server) humaGetLogPage(ctx context.Context, input *LogPageInput) (*LogPageOutput, error) {
	logPath, run, err := srv.resolveLogPath(ctx, input.RunID)
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

	// Annotate anchor lines so a viewer paging a finished run from disk (not just
	// the live SSE path) can tell which lines have rewindable frame history.
	frameCounts := logutil.ReadFrameHistoryCounts(logPath)
	entries := make([]LogLineEntry, len(lines))
	for i, l := range lines {
		entries[i] = LogLineEntry{
			N:          l.LineNum,
			Stream:     l.Stream,
			Text:       l.Text,
			FrameCount: frameCounts[l.LineNum],
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

// LogLineHistoryInput drives
// GET /api/runs/{runId}/log/line/{lineNumber}/history.
type LogLineHistoryInput struct {
	RunID      string `path:"runId" minLength:"26" maxLength:"26" pattern:"^[0-9A-HJKMNP-TV-Z]{26}$" doc:"Run ULID"`
	LineNumber int64  `path:"lineNumber" minimum:"0" doc:"Anchor line number to fetch frame history for"`
}

// LogLineHistoryBody returns the prior whole-region frames a progress bar or
// multi-line redraw passed through before it settled into the committed line.
// Frames are ordered oldest-first; each frame is the whole region (one or more
// rows) at that instant. The committed final state is not included. Empty when
// the line has no recorded history.
type LogLineHistoryBody struct {
	Frames [][]string `json:"frames" doc:"Prior whole-region frames, oldest first; each frame is one or more rows"`
}

type LogLineHistoryOutput struct {
	Body LogLineHistoryBody
}

func (srv *Server) humaGetLogLineHistory(ctx context.Context, input *LogLineHistoryInput) (*LogLineHistoryOutput, error) {
	logPath, _, err := srv.resolveLogPath(ctx, input.RunID)
	if err != nil {
		return nil, err
	}

	frames, _ := logutil.ReadFrameHistory(logPath, input.LineNumber)
	if frames == nil {
		frames = [][]string{}
	}
	return &LogLineHistoryOutput{Body: LogLineHistoryBody{Frames: frames}}, nil
}

func (srv *Server) humaGetLogRaw(ctx context.Context, input *LogRawInput) (*LogRawOutput, error) {
	logPath, _, err := srv.resolveLogPath(ctx, input.RunID)
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
	if prev, err := os.ReadFile(logutil.PrevPath(logPath)); err == nil {
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
	RunID       string `path:"runId" minLength:"26" maxLength:"26" pattern:"^[0-9A-HJKMNP-TV-Z]{26}$" doc:"Run ULID"`
	From        int64  `query:"from" default:"-1000" doc:"Anchor line number; 0 is the first line, negative values count from end (default -1000)"`
	ReplayLimit int64  `query:"replayLimit" minimum:"1" maximum:"50000" doc:"Cap on backfilled lines (default 5000)"`
	LastEventID string `header:"Last-Event-ID" doc:"Native SSE resume cursor; takes precedence over the from query"`
}

func (srv *Server) registerLogSSE(api huma.API) {
	sse.Register(api, huma.Operation{
		OperationID: "streamLog",
		Method:      http.MethodGet,
		Path:        "/api/runs/{runId}/log/stream",
		Summary:     "Stream a run's log lines as SSE",
		Description: "Server-Sent Events stream of absolute-line-numbered log entries. Replays history starting at `from` (or `Last-Event-ID + 1`), then follows live output until the run terminates.",
		Tags:        []string{"Logs"},
	}, map[string]any{
		"line":    LogLineSSEEvent{},
		"region":  LogRegionSSEEvent{},
		"rotated": LogRotatedEvent{},
		"dropped": LogDroppedEvent{},
		"done":    LogDoneEvent{},
	}, func(ctx context.Context, input *LogStreamInput, send sse.Sender) {
		release, ok := srv.streams.acquire(streamClientIPFromCtx(ctx))
		if !ok {
			return
		}
		defer release()

		run, err := srv.getRunByID(ctx, input.RunID)
		if err != nil {
			if !errors.Is(err, errInvalidRunID) {
				slog.Error("Failed to get run", "run", input.RunID, "err", err)
			}
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
			Region:  func(e logstream.RegionEvent) any { return LogRegionSSEEvent(e) },
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
