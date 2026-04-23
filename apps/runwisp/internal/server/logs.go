// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/logutil"
	"github.com/runwisp/runwisp/internal/model"
	"log/slog"
)

const (
	MaxLogBytes         = 100 * 1024 * 1024 // 100MB max bytes per request
	LogReadBufferSize   = 4 * 1024          // 4KB buffer for reading logs
	LogStreamBufferSize = 1 * 1024 * 1024   // 1MB max per stream read
	LogStreamInterval   = 500 * time.Millisecond
	LogStreamTimeout    = 10 * time.Minute
	LogFileWaitTimeout  = 30 * time.Second
)

// resolveLogPath validates the run ID and computes the log path.
func (srv *Server) resolveLogPath(resp http.ResponseWriter, req *http.Request) (string, *model.Run, bool) {
	runIDStr := chi.URLParam(req, "runId")
	if _, err := ulid.Parse(runIDStr); err != nil {
		respondBadRequest(resp, "Invalid run ID")
		return "", nil, false
	}

	run, err := srv.db.GetRun(runIDStr)
	if err != nil {
		slog.Error("Failed to get run", "run", runIDStr, "err", err)
		respondNotFound(resp, "Run not found")
		return "", nil, false
	}

	logPath := logutil.ResolveRunLogPath(srv.logDir, run.TaskName, run.ID, run.CreatedAt)
	return logPath, run, true
}

func (srv *Server) handleGetLog(resp http.ResponseWriter, req *http.Request) {
	logPath, _, ok := srv.resolveLogPath(resp, req)
	if !ok {
		return
	}

	startLineStr := req.URL.Query().Get("start_line")
	endLineStr := req.URL.Query().Get("end_line")

	// Line-based serving with index
	if startLineStr != "" {
		srv.serveLogByLines(resp, logPath, startLineStr, endLineStr)
		return
	}

	// Full file serving
	http.ServeFile(resp, req, logPath)
}

func (srv *Server) serveLogByLines(resp http.ResponseWriter, logPath, startLineStr, endLineStr string) {
	idxPath := logPath + ".idx"
	indices, err := logutil.ReadLogIndex(idxPath)
	if err != nil {
		slog.Warn("Failed to read log index", "path", idxPath, "err", err)
		respondNotFound(resp, "Log index not found")
		return
	}

	meta := logutil.ReadLogMeta(logPath)

	startLine, err := strconv.Atoi(startLineStr)
	if err != nil || startLine < 0 {
		startLine = 0
	}

	file, err := os.Open(logPath)
	if err != nil {
		respondError(resp, http.StatusInternalServerError, "Failed to open log file", err)
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		respondError(resp, http.StatusInternalServerError, "Failed to stat log file", err)
		return
	}
	fileSize := stat.Size()

	totalLines := logutil.CalculateTotalLines(file, indices, fileSize, meta)

	// Clamp start to first available line when earlier lines were rotated away
	firstAvailable := int(meta.RotatedLines)
	if startLine < firstAvailable {
		startLine = firstAvailable
	}

	realStartOffset := logutil.CalculateLineOffset(file, indices, startLine, meta)
	realEndOffset := fileSize
	if endLineStr != "" {
		endLine, err := strconv.Atoi(endLineStr)
		if err == nil && endLine >= 0 && endLine < totalLines {
			// end_line is inclusive; compute the start of the NEXT line
			// to capture the full content of endLine.
			realEndOffset = logutil.CalculateLineOffset(file, indices, endLine+1, meta)
		}
	}

	resp.Header().Set("X-Total-Lines", strconv.Itoa(totalLines))
	if meta.RotatedLines > 0 {
		resp.Header().Set("X-First-Available-Line", strconv.Itoa(firstAvailable))
		resp.Header().Set("X-Truncated", "true")
	}

	length := realEndOffset - realStartOffset
	if length < 0 {
		length = 0
	}

	serveLogRange(resp, file, realStartOffset, length, fileSize)
}

func (srv *Server) handleLogStream(resp http.ResponseWriter, req *http.Request) {
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

	// Extend the write deadline for this long-lived SSE connection.
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

	streamer, err := newLogStreamer(ctx, resp, flusher, logPath)
	if err != nil {
		fmt.Fprintf(resp, "data: Error opening log: %v\n\n", err)
		flusher.Flush()
		return
	}
	defer streamer.Close()

	virtualSize, physSize, err := streamer.virtualFileSize()
	if err != nil {
		fmt.Fprintf(resp, "data: Error getting file info: %v\n\n", err)
		flusher.Flush()
		return
	}

	streamer.emitMetadata(virtualSize)

	offsetStr := req.URL.Query().Get("offset")
	remaining, err := streamer.seekToOffset(offsetStr, virtualSize, physSize)
	if err != nil {
		slog.Error("Failed to seek in log file", "err", err)
		return
	}

	if err := streamer.emitInitialData(remaining); err != nil {
		slog.Error("Failed to read log file", "err", err)
		return
	}

	if run.Status != model.PhaseEnded {
		slog.Debug("SSE: entering pollLoop", "runID", run.ID, "status", run.Status, "logPath", logPath, "initLastSize", streamer.lastSize)
		streamer.pollLoop(ctx, run.ID, srv.eventBus, srv.db)
		slog.Debug("SSE: pollLoop exited", "runID", run.ID)
	} else {
		slog.Debug("SSE: run already ended, emitting done immediately", "runID", run.ID, "logPath", logPath)
		streamer.emitDone()
	}
}

func serveLogRange(w http.ResponseWriter, file *os.File, offset, maxBytes, fileSize int64) {
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to seek in log file", err)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-File-Size", strconv.FormatInt(fileSize, 10))
	w.Header().Set("X-Content-Offset", strconv.FormatInt(offset, 10))

	if maxBytes > 0 {
		data, err := logutil.ReadWithLineBoundaries(file, maxBytes)
		if err != nil {
			respondError(w, http.StatusInternalServerError, "Failed to read log file", err)
			return
		}

		if len(data) == 0 && offset >= fileSize {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if _, err := w.Write(data); err != nil {
			slog.Error("Failed to write log data", "err", err)
		}
	} else {
		if _, err := io.Copy(w, file); err != nil {
			slog.Error("Failed to copy log data", "err", err)
		}
	}
}
