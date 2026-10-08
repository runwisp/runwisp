// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/go-chi/chi/v5"
)

//go:embed all:dist
var uiFiles embed.FS

const (
	indexHTML   = "index.html"
	errInternal = "internal error"
)

// Mount registers a handler serving the embedded Svelte dashboard on the given
// chi router under "/*". The embedded FS is opened once at mount time; daemon
// startup fails fast if the embedded asset tree is malformed instead of
// panicking inside an HTTP request hot path.
func Mount(router chi.Router) error {
	stripped, err := fs.Sub(uiFiles, "dist")
	if err != nil {
		return fmt.Errorf("ui: open embedded dist: %w", err)
	}
	router.Get("/*", func(w http.ResponseWriter, req *http.Request) {
		serve(stripped, w, req)
	})
	return nil
}

func serve(stripped fs.FS, w http.ResponseWriter, req *http.Request) {
	// path.Clean collapses any ".." segments before we touch the FS (embed.FS.Open
	// also rejects invalid paths).
	reqPath := strings.TrimPrefix(path.Clean("/"+req.URL.Path), "/")
	if reqPath == "" || reqPath == "." {
		reqPath = indexHTML
	}

	// An unknown API path must answer like the API (a JSON error), not hand a
	// client the SPA shell with a 200.
	if reqPath == "api" || strings.HasPrefix(reqPath, "api/") {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"title":"Not Found","status":404,"detail":"No such API route"}`)
		return
	}

	if tryServeFile(stripped, w, req, reqPath) {
		return
	}

	// A path with an extension is only a missing asset when the request does
	// not accept HTML. Browser navigations (refresh, direct URL entry) always
	// send Accept: text/html — and task names may legally contain dots, so
	// /tasks/backup.daily must still reach the SPA fallback.
	acceptsHTML := strings.Contains(req.Header.Get("Accept"), "text/html")
	if strings.HasPrefix(reqPath, "_app/") || (path.Ext(reqPath) != "" && !acceptsHTML) {
		http.NotFound(w, req)
		return
	}

	// SPA fallback: every other unmatched path gets the root index.html.
	if !tryServeFile(stripped, w, req, indexHTML) {
		http.NotFound(w, req)
	}
}

// tryServeFile attempts to serve reqPath from the embedded FS. Returns true
// if the request was handled (even with an error response), false if the
// caller should fall through to the SPA index fallback.
func tryServeFile(stripped fs.FS, w http.ResponseWriter, req *http.Request, reqPath string) bool {
	// fs.ValidPath is the same guard embed.FS.Open applies internally: it
	// rejects any name with a "..", leading "/", or "." segment, so a
	// user-controlled reqPath cannot escape the read-only embedded tree. The
	// //NOSONAR sits on the Open call — the exact sink gosecurity:S2083 flags —
	// so the suppression stays glued to the sink if this function is ever moved.
	if !fs.ValidPath(reqPath) {
		return false
	}
	f, err := stripped.Open(reqPath) //NOSONAR: reqPath is path.Clean'd + fs.ValidPath-checked and confined to a read-only embed.FS
	if err != nil {
		return false
	}
	defer f.Close()

	stat, statErr := f.Stat()
	if statErr != nil {
		http.Error(w, errInternal, http.StatusInternalServerError)
		return true
	}
	if stat.IsDir() {
		return false
	}

	// Regular files from embed.FS are always seekable. ServeContent sets
	// Content-Type from the extension (sniffing when it has none).
	http.ServeContent(w, req, reqPath, stat.ModTime(), f.(io.ReadSeeker))
	return true
}
