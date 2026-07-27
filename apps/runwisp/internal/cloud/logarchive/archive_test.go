// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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
	var gotEncoding, gotType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("got method=%q want PUT", r.Method)
		}
		gotEncoding = r.Header.Get("Content-Encoding")
		gotType = r.Header.Get("Content-Type")
		if _, err := io.Copy(&receivedBody, r.Body); err != nil {
			t.Fatalf("copy body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	size, err := archive(context.Background(), srv.Client(), srv.URL, logPath)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if size <= 0 {
		t.Errorf("size=%d want >0", size)
	}

	// The body is gzip-compressed but advertised as gzip-encoded text so a
	// browser fetching the signed GET URL auto-decompresses it to plain text.
	if gotEncoding != "gzip" {
		t.Errorf("Content-Encoding=%q want gzip", gotEncoding)
	}
	if gotType != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type=%q want text/plain; charset=utf-8", gotType)
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

	if _, err := archive(context.Background(), srv.Client(), srv.URL, logPath); err != nil {
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

	_, err := archive(context.Background(), srv.Client(), srv.URL, logPath)
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

func TestValidateUploadURL(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"https public host", "https://bucket.s3.amazonaws.com/key?sig=abc", false},
		{"https public host with port", "https://s3.example.com:443/key", false},
		{"http rejected", "http://bucket.s3.amazonaws.com/key", true},
		{"empty scheme rejected", "bucket.s3.amazonaws.com/key", true},
		{"no host rejected", "https:///key", true},
		{"empty rejected", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateUploadURL(c.url)
			if c.wantErr && err == nil {
				t.Fatalf("validateUploadURL(%q) = nil, want error", c.url)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("validateUploadURL(%q) = %v, want nil", c.url, err)
			}
		})
	}
}

func TestArchiveRejectsNonHTTPSURL(t *testing.T) {
	logPath := writeLog(t, "secret log data")
	// A non-https URL is rejected as a permanent failure before any network
	// call, so the daemon can't be used as a plaintext SSRF egress.
	_, err := Archive(context.Background(), nil, "http://169.254.169.254/steal", logPath)
	if err == nil {
		t.Fatal("expected error for non-https URL")
	}
	var perm *PermanentError
	if !errors.As(err, &perm) {
		t.Fatalf("expected PermanentError, got %v", err)
	}
}

func TestSafeClientRejectsInternalAddress(t *testing.T) {
	// The hardened client's dialer must refuse to connect to internal
	// addresses even when the URL passed scheme/host validation — this is the
	// guard for IP-literal and DNS-rebind SSRF targets.
	client := SafeClient()
	for _, target := range []string{
		"https://127.0.0.1:9/key",
		"https://169.254.169.254/latest/meta-data",
		"https://10.0.0.1/key",
	} {
		req, err := http.NewRequest(http.MethodPut, target, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		if _, err := client.Do(req); err == nil {
			t.Fatalf("expected dial rejection for %q", target)
		}
	}
}

func TestPermanentError_Error(t *testing.T) {
	e := &PermanentError{StatusCode: 403, Status: "403 Forbidden"}
	msg := e.Error()
	if msg == "" {
		t.Fatal("expected non-empty error message")
	}
	if msg != "logarchive: permanent failure: 403 Forbidden" {
		t.Fatalf("unexpected error message: %q", msg)
	}
}
