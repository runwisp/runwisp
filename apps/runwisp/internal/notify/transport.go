// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HTTPProvider wraps an http.Client with backoff + Retry-After handling for the
// HTTP-based channels. The 429 body inspector is provided by the caller
// (Telegram exposes parameters.retry_after in JSON).
type HTTPProvider struct {
	Client    *http.Client
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

// Post posts body with backoff + retry-after honoring, merging extra (e.g.
// operator-configured auth headers) into the request headers. Returns nil on
// 2xx, a permanent error on 4xx (except 408/429), and the last transport
// error on backoff exhaustion.
func (p *HTTPProvider) Post(ctx context.Context, url, contentType string, body []byte, extra http.Header) error {
	return RetryWithBackoff(ctx, p.Backoff, func(ctx context.Context) error {
		return p.doHTTPRequest(ctx, url, contentType, body, extra)
	})
}

func (p *HTTPProvider) doHTTPRequest(ctx context.Context, url, contentType string, body []byte, extra http.Header) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Permanent(err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", p.UserAgent)
	for k, vals := range extra {
		for _, v := range vals {
			req.Header.Add(k, v)
		}
	}
	// A copy, so the redirect policy holds whatever Client was injected.
	client := *p.Client
	client.CheckRedirect = replayPostRedirect
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	return p.checkHTTPResponse(ctx, resp.StatusCode, resp.Header, respBody)
}

// replayPostRedirect follows only the redirects that resend the POST and its
// body (307, 308). net/http turns a 301/302/303 into a body-less GET, which
// the endpoint typically answers 200 (an http:// URL that redirects to https://),
// so the notification would be lost while the delivery read as a success. Those
// are left unfollowed and surface as a permanent error from checkHTTPResponse.
func replayPostRedirect(req *http.Request, via []*http.Request) error {
	if c := req.Response.StatusCode; c != http.StatusTemporaryRedirect && c != http.StatusPermanentRedirect {
		return http.ErrUseLastResponse
	}
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return nil
}

func (p *HTTPProvider) checkHTTPResponse(ctx context.Context, statusCode int, header http.Header, body []byte) error {
	switch {
	case statusCode >= 200 && statusCode < 300:
		return nil
	case statusCode == http.StatusTooManyRequests:
		return p.handleRateLimit(ctx, statusCode, header, body)
	case statusCode >= 300 && statusCode < 400:
		return Permanent(fmt.Errorf("permanent: status=%d redirected to %q; set the notifier's url to the final address", statusCode, header.Get("Location")))
	case IsPermanentHTTPStatus(statusCode):
		return Permanent(fmt.Errorf("permanent: status=%d body=%s", statusCode, truncateBody(body)))
	default:
		return fmt.Errorf("transient: status=%d body=%s", statusCode, truncateBody(body))
	}
}

func (p *HTTPProvider) handleRateLimit(ctx context.Context, statusCode int, header http.Header, body []byte) error {
	d := ParseRetryAfterHeader(header)
	if d == 0 && p.Body429Fn != nil {
		d = p.Body429Fn(body)
	}
	d = clampRetryAfter(d, p.Backoff)
	// Hand the server-supplied delay to the retry loop rather than sleeping
	// here, so there is a single wait and it honors ctx.Done() (see
	// SetNextRetryInterval).
	SetNextRetryInterval(ctx, d)
	return fmt.Errorf("rate-limited: status=%d body=%s", statusCode, truncateBody(body))
}

func truncateBody(b []byte) string {
	const maxLen = 200
	if len(b) <= maxLen {
		return string(b)
	}
	return string(b[:maxLen]) + "…"
}
