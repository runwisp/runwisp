// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	indexBody     = "<html>index</html>"
	browserAccept = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"
)

func testFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html":     {Data: []byte(indexBody)},
		"favicon.svg":    {Data: []byte("<svg/>")},
		"_app/chunk.js":  {Data: []byte("console.log(1)")},
		"tasks/sub/x.js": {Data: []byte("console.log(2)")},
	}
}

func TestServe(t *testing.T) {
	tests := []struct {
		name            string
		path            string
		accept          string
		wantStatus      int
		wantBody        string
		wantContentType string
	}{
		{
			name:       "root serves index",
			path:       "/",
			wantStatus: http.StatusOK,
			wantBody:   indexBody,
		},
		{
			name:       "existing asset served with content type",
			path:       "/_app/chunk.js",
			wantStatus: http.StatusOK,
			wantBody:   "console.log(1)",
			// mime.TypeByExtension may append a charset; checked via prefix below.
			wantContentType: "text/javascript",
		},
		{
			name:       "extensionless SPA route falls back to index",
			path:       "/tasks/my-task",
			wantStatus: http.StatusOK,
			wantBody:   indexBody,
		},
		{
			name:       "navigation to dotted task name falls back to index",
			path:       "/tasks/backup.daily",
			accept:     browserAccept,
			wantStatus: http.StatusOK,
			wantBody:   indexBody,
		},
		{
			name:       "task name with colon falls back to index",
			path:       "/tasks/db:backup",
			wantStatus: http.StatusOK,
			wantBody:   indexBody,
		},
		{
			name:       "navigation to task name with colon and dot falls back to index",
			path:       "/tasks/db:backup.v2",
			accept:     browserAccept,
			wantStatus: http.StatusOK,
			wantBody:   indexBody,
		},
		{
			name:       "missing _app asset is 404 even for navigations",
			path:       "/_app/missing.js",
			accept:     browserAccept,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "missing asset fetch is 404",
			path:       "/favicon.png",
			accept:     "image/avif,image/webp,*/*",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "missing extension path without HTML accept is 404",
			path:       "/tasks/backup.daily",
			accept:     "*/*",
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "traversal is collapsed and confined to the embedded FS",
			path:       "/../../etc/passwd",
			wantStatus: http.StatusOK,
			wantBody:   indexBody,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.accept != "" {
				req.Header.Set("Accept", tt.accept)
			}
			rec := httptest.NewRecorder()

			serve(testFS(), rec, req)

			require.Equal(t, tt.wantStatus, rec.Code)
			if tt.wantBody != "" {
				assert.Equal(t, tt.wantBody, rec.Body.String())
			}
			if tt.wantContentType != "" {
				assert.True(t, strings.HasPrefix(rec.Header().Get("Content-Type"), tt.wantContentType),
					"Content-Type %q does not start with %q", rec.Header().Get("Content-Type"), tt.wantContentType)
			}
		})
	}
}

func TestServeMissingIndexIs404(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/tasks/my-task", nil)
	rec := httptest.NewRecorder()

	serve(fstest.MapFS{}, rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
}
