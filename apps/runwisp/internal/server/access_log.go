// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// slogAccessLogger is chi's middleware.LogFormatter routed through slog so HTTP
// access lines share format, level handling, and destination with the rest of
// the daemon's logging. Quiet requests (status < 500) emit at DEBUG so a
// default INFO daemon stays silent under normal traffic — /health self-polls,
// dashboard 401 polls, and SSE keepalives no longer fight the banner for
// attention. 5xx is promoted to WARN so real failures surface without an
// operator having to lower the log level.
type slogAccessLogger struct{}

// NewLogEntry snapshots the request fields we want to log at request-entry
// time. The returned entry's Write method is deferred until the response is
// finished, by which time r may have been mutated, so we capture them up front.
func (slogAccessLogger) NewLogEntry(r *http.Request) middleware.LogEntry {
	return &slogAccessEntry{
		ctx:    r.Context(),
		method: r.Method,
		path:   r.URL.RequestURI(),
		remote: r.RemoteAddr,
	}
}

type slogAccessEntry struct {
	ctx    context.Context
	method string
	path   string
	remote string
}

// Write emits the access record. Levels:
//   - <500 → DEBUG (hidden at default --log-level=info)
//   - 5xx  → WARN (visible by default; real server-side failure)
//
// The `extra` argument is chi's hook for handler-supplied detail; we ignore
// it — current routes don't use it and including it raw would defeat slog's
// structured shape.
func (e *slogAccessEntry) Write(status, bytes int, _ http.Header, elapsed time.Duration, _ interface{}) {
	level := slog.LevelDebug
	if status >= http.StatusInternalServerError {
		level = slog.LevelWarn
	}
	slog.Log(e.ctx, level, "http",
		"method", e.method,
		"path", e.path,
		"status", status,
		"dur", elapsed.Round(time.Microsecond),
		"bytes", bytes,
		"remote", e.remote,
	)
}

// Panic surfaces handler panics at ERROR so they always reach the operator.
// chi's middleware.Recoverer still runs and returns 500 to the client; this
// line is the structured complement to the stack trace Recoverer prints.
func (e *slogAccessEntry) Panic(v interface{}, _ []byte) {
	slog.Log(e.ctx, slog.LevelError, "http panic",
		"method", e.method,
		"path", e.path,
		"remote", e.remote,
		"err", v,
	)
}
