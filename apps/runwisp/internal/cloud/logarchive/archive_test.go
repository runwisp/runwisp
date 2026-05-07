// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package logarchive

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func writeLog(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.log")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write log: %v", err)
	}
	return path
}

func TestArchiveSuccess(t *testing.T) {
	logPath := writeLog(t, "hello world\nline two\n")

	var receivedBody bytes.Buffer
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("got method=%q want PUT", r.Method)
		}
		if _, err := io.Copy(&receivedBody, r.Body); err != nil {
			t.Fatalf("copy body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	size, err := Archive(context.Background(), srv.Client(), srv.URL, logPath)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if size <= 0 {
		t.Errorf("size=%d want >0", size)
	}

	gz, err := gzip.NewReader(&receivedBody)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gz.Close()
	got, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("read gunzipped: %v", err)
	}
	if string(got) != "hello world\nline two\n" {
		t.Errorf("got %q", string(got))
	}
}

func TestArchiveRetriesOn5xx(t *testing.T) {
	logPath := writeLog(t, "data")
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n < 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if _, err := Archive(context.Background(), srv.Client(), srv.URL, logPath); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if got := attempts.Load(); got != 2 {
		t.Errorf("attempts=%d want 2", got)
	}
}

func TestArchivePermanentError(t *testing.T) {
	logPath := writeLog(t, "data")
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := Archive(context.Background(), srv.Client(), srv.URL, logPath)
	if err == nil {
		t.Fatal("expected error")
	}
	var perm *PermanentError
	if !errors.As(err, &perm) {
		t.Errorf("expected PermanentError, got %v", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts=%d want 1 (no retry on 4xx)", got)
	}
}

func TestArchiveEmptyURL(t *testing.T) {
	logPath := writeLog(t, "x")
	if _, err := Archive(context.Background(), nil, "", logPath); err == nil {
		t.Fatal("expected error")
	}
}
