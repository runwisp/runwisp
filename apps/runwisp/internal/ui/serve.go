// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package ui

import (
	"embed"
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

// Serve mounts the embedded UI assets.
func Serve(router chi.Router) {
	stripped, err := fs.Sub(uiFiles, "dist")
	if err != nil {
		panic("ui: failed to open embedded dist: " + err.Error())
	}

	router.Get("/*", func(w http.ResponseWriter, req *http.Request) {
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
	})
}
