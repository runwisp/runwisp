// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDaemonLogBuffer_DoesNotCapturePasswordValue is the regression test for
// the password's most plausible leakage path: the daemon routes slog through
// io.MultiWriter(stderr, DaemonLogBuffer), so anything the credentials handler
// logs is exposed to remote TUI clients over SSE. A future change that
// attaches the password value to the audit line would surface it on every
// `tui --remote` connection. Locking the buffer's contents catches that.
func TestDaemonLogBuffer_DoesNotCapturePasswordValue(t *testing.T) {
	const sentinel = "BufferLeakSentinel-7q"

	logBuf := NewDaemonLogBuffer(64)
	routeSlogTo(t, logBuf)

	s := newServerForCredentialsTest(t, sentinel, true)
	req := httptest.NewRequest("GET", "/api/local/credentials", nil)
	req = req.WithContext(context.WithValue(req.Context(), localTrustedKey{}, true))
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	lines := logBuf.Lines(64)
	joined := strings.Join(lines, "\n")
	assert.Contains(t, joined, "Ephemeral password retrieved via socket",
		"the audit message must still be observable via the SSE-streamed daemon log")
	assert.NotContains(t, joined, sentinel,
		"the password value must never reach DaemonLogBuffer — it streams to remote TUI clients")
}
