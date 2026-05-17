// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cenkalti/backoff/v4"
)

// HTTPDoer is the minimal http.Client surface the transport requires. Lets
// tests inject a stub.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// HTTPProvider wraps a HTTPDoer with backoff + Retry-After handling. Used by
// Slack and Telegram channels. The 429 body inspector is provided by the
// caller (Telegram exposes parameters.retry_after in JSON).
type HTTPProvider struct {
	Client    HTTPDoer
	Backoff   BackoffConfig
	Body429Fn func(body []byte) time.Duration // optional; returns 0 if not present
	UserAgent string
}

// NewHTTPProvider returns a transport with the daemon's default backoff.
func NewHTTPProvider() *HTTPProvider {
	return &HTTPProvider{
		Client:    &http.Client{Timeout: 15 * time.Second},
		Backoff:   DefaultBackoff(),
		UserAgent: "runwisp-notify/1",
	}
}

// PostJSON posts body with backoff + retry-after honoring. Returns nil on
// 2xx, a permanent error on 4xx (except 408/429), and the last transport
// error on backoff exhaustion.
func (p *HTTPProvider) PostJSON(ctx context.Context, url string, contentType string, body []byte) error {
	op := func() error {
		return p.doHTTPRequest(ctx, url, contentType, body)
	}

	bo := p.Backoff.NewExponential()
	bo.Reset()
	wrapped := backoff.WithContext(bo, ctx)
	if err := backoff.Retry(op, wrapped); err != nil {
		var perm *backoff.PermanentError
		if errors.As(err, &perm) {
			return perm.Err
		}
		return err
	}
	return nil
}

func (p *HTTPProvider) doHTTPRequest(ctx context.Context, url, contentType string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return backoff.Permanent(err)
	}
	req.Header.Set("Content-Type", contentType)
	if p.UserAgent != "" {
		req.Header.Set("User-Agent", p.UserAgent)
	}
	resp, err := p.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	return p.checkHTTPResponse(ctx, resp.StatusCode, resp.Header, respBody)
}

func (p *HTTPProvider) checkHTTPResponse(ctx context.Context, statusCode int, header http.Header, body []byte) error {
	switch {
	case statusCode >= 200 && statusCode < 300:
		return nil
	case statusCode == http.StatusTooManyRequests:
		return p.handleRateLimit(ctx, statusCode, header, body)
	case IsPermanentHTTPStatus(statusCode):
		return backoff.Permanent(fmt.Errorf("permanent: status=%d body=%s", statusCode, truncateBody(body)))
	default:
		return fmt.Errorf("transient: status=%d body=%s", statusCode, truncateBody(body))
	}
}

func (p *HTTPProvider) handleRateLimit(ctx context.Context, statusCode int, header http.Header, body []byte) error {
	d := ParseRetryAfterHeader(header)
	if d == 0 && p.Body429Fn != nil {
		d = p.Body429Fn(body)
	}
	if d > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
		}
	}
	return fmt.Errorf("rate-limited: status=%d body=%s", statusCode, truncateBody(body))
}

func truncateBody(b []byte) string {
	const max = 200
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "…"
}
