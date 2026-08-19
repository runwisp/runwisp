// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// captureSlog points the default logger at a buffer at DEBUG level so we can
// observe every record, including ones our formatter emits below INFO. The
// previous logger is restored on cleanup.
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf
}

// TestSlogAccessLogger_LevelByStatus is the core contract: HTTP traffic is
// background noise by default and must not bury slog INFO output, but server
// errors are real failures and must surface without lowering the log level.
func TestSlogAccessLogger_LevelByStatus(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		wantLevel string
	}{
		{"health 200 is DEBUG", http.StatusOK, "level=DEBUG"},
		{"poller 401 is DEBUG", http.StatusUnauthorized, "level=DEBUG"},
		{"404 is DEBUG", http.StatusNotFound, "level=DEBUG"},
		{"500 is WARN", http.StatusInternalServerError, "level=WARN"},
		{"503 is WARN", http.StatusServiceUnavailable, "level=WARN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf := captureSlog(t)
			req := httptest.NewRequest(http.MethodGet, "/api/system", nil)
			req.RemoteAddr = "127.0.0.1:54321"

			entry := slogAccessLogger{}.NewLogEntry(req)
			entry.Write(tc.status, 22, nil, 100*time.Microsecond, nil)

			out := buf.String()
			assert.Contains(t, out, tc.wantLevel)
			assert.Contains(t, out, `msg=http`)
			assert.Contains(t, out, "method=GET")
			assert.Contains(t, out, "path=/api/system")
			assert.Contains(t, out, "remote=127.0.0.1:54321")
		})
	}
}

// TestSlogAccessLogger_SnapshotIsRequestEntryTime locks that NewLogEntry
// snapshots r.RemoteAddr at request entry — chi defers entry.Write until after
// downstream middleware (notably middleware.RealIP) may have rewritten r —
// so the access log must always show the raw peer regardless of later XFF.
func TestSlogAccessLogger_SnapshotIsRequestEntryTime(t *testing.T) {
	buf := captureSlog(t)
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/foo/run", nil)
	req.RemoteAddr = "10.0.0.5:12345"

	entry := slogAccessLogger{}.NewLogEntry(req)
	// Mutate r after NewLogEntry — should NOT influence the eventual log line.
	req.RemoteAddr = "203.0.113.7:80"
	entry.Write(http.StatusOK, 0, nil, time.Millisecond, nil)

	out := buf.String()
	assert.Contains(t, out, "remote=10.0.0.5:12345")
	assert.NotContains(t, out, "203.0.113.7",
		"the entry must capture remote at NewLogEntry time so RealIP can't rewrite the audit trail")
}

// TestSlogAccessLogger_Panic emits at ERROR so handler panics are always
// visible regardless of --log-level.
func TestSlogAccessLogger_Panic(t *testing.T) {
	buf := captureSlog(t)
	req := httptest.NewRequest(http.MethodGet, "/api/daemon", nil)
	req.RemoteAddr = "127.0.0.1:54321"

	entry := slogAccessLogger{}.NewLogEntry(req)
	entry.Panic("boom", nil)

	out := buf.String()
	assert.Contains(t, out, "level=ERROR")
	assert.Contains(t, out, `msg="http panic"`)
	assert.Contains(t, out, "err=boom")
}
