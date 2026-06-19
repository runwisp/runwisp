// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

// Package logarchive uploads a single gzipped log file to a signed PUT URL
// supplied by the control plane in the dispatch payload. It is intentionally
// stateless: callers track which executions still need archiving.
package logarchive

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// MaxAttempts caps the retries on transient PUT failures.
const MaxAttempts = 3

// Archive gzips logFilePath and PUTs it to uploadURL. Returns the gzipped
// byte count on success. Retries up to MaxAttempts on network errors and
// 5xx responses with exponential backoff.
func Archive(ctx context.Context, client *http.Client, uploadURL, logFilePath string) (int64, error) {
	if client == nil {
		client = http.DefaultClient
	}
	if uploadURL == "" {
		return 0, errors.New("logarchive: empty upload URL")
	}

	body, size, err := gzipFile(logFilePath)
	if err != nil {
		return 0, err
	}

	var lastErr error
	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		err := putOnce(ctx, client, uploadURL, body)
		if err == nil {
			return size, nil
		}
		lastErr = err
		var perm *PermanentError
		if errors.As(err, &perm) {
			return 0, err
		}
		if attempt < MaxAttempts {
			backoff := time.Duration(1<<attempt) * time.Second
			select {
			case <-ctx.Done():
				return 0, ctx.Err()
			case <-time.After(backoff):
			}
		}
	}
	return 0, fmt.Errorf("logarchive: upload failed after %d attempts: %w", MaxAttempts, lastErr)
}

func gzipFile(path string) ([]byte, int64, error) {
	src, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("logarchive: open log file: %w", err)
	}
	defer src.Close()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := io.Copy(gz, src); err != nil {
		return nil, 0, fmt.Errorf("logarchive: gzip log file: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, 0, fmt.Errorf("logarchive: close gzip writer: %w", err)
	}
	return buf.Bytes(), int64(buf.Len()), nil
}

func putOnce(ctx context.Context, client *http.Client, url string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build PUT request: %w", err)
	}
	// The body is gzip-compressed, but advertise it as gzip-encoded text rather
	// than an application/gzip blob: S3/MinIO stores and replays these headers,
	// so a browser fetching the signed GET URL transparently decompresses the
	// archive and renders the log as plain text. (The presigned PUT signs only
	// host — Content-Type/Content-Encoding are unsigned, so setting them here
	// does not break the signature.)
	req.Header.Set("Content-Encoding", "gzip")
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
	req.ContentLength = int64(len(body))

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return &PermanentError{StatusCode: resp.StatusCode, Status: resp.Status}
	}
	return fmt.Errorf("upload failed: %s", resp.Status)
}

// PermanentError signals a non-retryable PUT response (4xx).
type PermanentError struct {
	StatusCode int
	Status     string
}

func (e *PermanentError) Error() string {
	return fmt.Sprintf("logarchive: permanent failure: %s", e.Status)
}
