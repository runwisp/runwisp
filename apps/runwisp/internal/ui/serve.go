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
	reqPath := strings.TrimPrefix(req.URL.Path, "/")
	if reqPath == "" {
		reqPath = "index.html"
	}

	f, err := stripped.Open(reqPath)
	if err == nil {
		defer f.Close()

		stat, statErr := f.Stat()
		if statErr != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !stat.IsDir() {
			if contentType := mime.TypeByExtension(path.Ext(reqPath)); contentType != "" {
				w.Header().Set("Content-Type", contentType)
			}
			rs, ok := f.(io.ReadSeeker)
			if !ok {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			http.ServeContent(w, req, reqPath, stat.ModTime(), rs)
			return
		}
	}

	if strings.HasPrefix(reqPath, "_app/") || (path.Ext(reqPath) != "" && reqPath != "index.html") {
		http.NotFound(w, req)
		return
	}

	indexFile, err := stripped.Open("index.html")
	if err != nil {
		http.NotFound(w, req)
		return
	}
	defer indexFile.Close()

	stat, statErr := indexFile.Stat()
	if statErr != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	rs, ok := indexFile.(io.ReadSeeker)
	if !ok {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, req, "index.html", stat.ModTime(), rs)
}
