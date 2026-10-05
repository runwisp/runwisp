// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/oklog/ulid/v2"
	"github.com/runwisp/runwisp/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// A refused stream must be a real error status: huma flushes a 200 before the
// stream callback runs, so a refusal made there is an empty text/event-stream.
func TestSSERoutes_RefusedPastStreamCap(t *testing.T) {
	for _, path := range []string{
		"/api/events/stream",
		"/api/daemon/log/stream",
		"/api/runs/" + ulid.Make().String() + "/log/stream",
	} {
		t.Run(path, func(t *testing.T) {
			s, _, _, _ := setupServer(t)
			s.streams = newStreamLimiter(0, 0)

			req := httptest.NewRequest(http.MethodGet, path, nil)
			addAuth(req, s)
			w := httptest.NewRecorder()
			s.router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusServiceUnavailable, w.Code)
			assert.NotEqual(t, "text/event-stream", w.Header().Get("Content-Type"))
		})
	}
}

func TestLogStream_UnknownRunIs404(t *testing.T) {
	s, repo, _, _ := setupServer(t)
	id := ulid.Make().String()
	repo.On("GetRun", mock.Anything, id).Return(nil, storage.ErrNotFound)

	req := httptest.NewRequest(http.MethodGet, "/api/runs/"+id+"/log/stream", nil)
	addAuth(req, s)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NotEqual(t, "text/event-stream", w.Header().Get("Content-Type"))
}

func TestLogStream_MalformedRunIDKeepsValidationError(t *testing.T) {
	s, _, _, _ := setupServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/runs/not-a-ulid/log/stream", nil)
	addAuth(req, s)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
}
