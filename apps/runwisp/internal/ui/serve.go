// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"mime"
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

// Handler returns an http.Handler that serves the embedded Svelte dashboard.
// The embedded FS is opened once at construction; if the embed is malformed
// the error is surfaced to the caller instead of panicking inside an HTTP
// request hot path.
func Handler() (http.Handler, error) {
	stripped, err := fs.Sub(uiFiles, "dist")
	if err != nil {
		return nil, fmt.Errorf("ui: open embedded dist: %w", err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		serve(stripped, w, req)
	}), nil
}

// Mount registers Handler on the given chi router under "/*". Daemon startup
// fails fast if the embedded asset tree is malformed.
func Mount(router chi.Router) error {
	h, err := Handler()
	if err != nil {
		return err
	}
	router.Get("/*", h.ServeHTTP)
	return nil
}

func serve(stripped fs.FS, w http.ResponseWriter, req *http.Request) {
	// path.Clean collapses any ".." segments before we touch the FS.
	// embed.FS.Open also rejects invalid paths, so traversal to real files is
	// impossible, but being explicit keeps static analysis tools happy.
	reqPath := strings.TrimPrefix(path.Clean("/"+req.URL.Path), "/") //NOSONAR: path is sanitized via path.Clean and restricted to embed.FS
	if reqPath == "" || reqPath == "." {
		reqPath = indexHTML
	}

	if tryServeFile(stripped, w, req, reqPath) {
		return
	}

	// A path with an extension is only a missing asset when the request does
	// not accept HTML. Browser navigations (refresh, direct URL entry) always
	// send Accept: text/html — and task names may legally contain dots, so
	// /tasks/backup.daily must still reach the SPA fallback.
	if strings.HasPrefix(reqPath, "_app/") || (path.Ext(reqPath) != "" && !acceptsHTML(req)) {
		http.NotFound(w, req)
		return
	}

	serveIndexFallback(stripped, w, req)
}

// acceptsHTML reports whether the request's Accept header includes text/html,
// i.e. it is a browser navigation rather than an asset fetch.
func acceptsHTML(req *http.Request) bool {
	return strings.Contains(req.Header.Get("Accept"), "text/html")
}

// tryServeFile attempts to serve reqPath from the embedded FS. Returns true
// if the request was handled (even with an error response), false if the
// caller should fall through to the SPA index fallback.
func tryServeFile(stripped fs.FS, w http.ResponseWriter, req *http.Request, reqPath string) bool {
	f, err := stripped.Open(reqPath)
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

	if contentType := mime.TypeByExtension(path.Ext(reqPath)); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		http.Error(w, errInternal, http.StatusInternalServerError)
		return true
	}
	http.ServeContent(w, req, reqPath, stat.ModTime(), rs)
	return true
}

// serveIndexFallback serves the SPA root index.html for any unmatched path.
func serveIndexFallback(stripped fs.FS, w http.ResponseWriter, req *http.Request) {
	indexFile, err := stripped.Open(indexHTML)
	if err != nil {
		http.NotFound(w, req)
		return
	}
	defer indexFile.Close()

	stat, statErr := indexFile.Stat()
	if statErr != nil {
		http.Error(w, errInternal, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	rs, ok := indexFile.(io.ReadSeeker)
	if !ok {
		http.Error(w, errInternal, http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, req, indexHTML, stat.ModTime(), rs)
}
