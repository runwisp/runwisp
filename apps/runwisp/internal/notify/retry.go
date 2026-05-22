// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
)

// BackoffConfig controls outbound retry behavior. Mirrors the public fields of
// cenkalti/backoff/v4 ExponentialBackOff for the subset we use.
type BackoffConfig struct {
	InitialInterval time.Duration
	MaxInterval     time.Duration
	MaxElapsedTime  time.Duration
	Multiplier      float64
}

// DefaultBackoff matches the values from the plan: 1s → 60s, 5m total budget.
func DefaultBackoff() BackoffConfig {
	return BackoffConfig{
		InitialInterval: time.Second,
		MaxInterval:     time.Minute,
		MaxElapsedTime:  5 * time.Minute,
		Multiplier:      2.0,
	}
}

// NewExponential constructs a configured cenkalti backoff.
func (c BackoffConfig) NewExponential() *backoff.ExponentialBackOff {
	b := backoff.NewExponentialBackOff()
	if c.InitialInterval > 0 {
		b.InitialInterval = c.InitialInterval
	}
	if c.MaxInterval > 0 {
		b.MaxInterval = c.MaxInterval
	}
	b.MaxElapsedTime = c.MaxElapsedTime
	if c.Multiplier > 0 {
		b.Multiplier = c.Multiplier
	}
	b.Reset()
	return b
}

// ParseRetryAfterHeader extracts a delay from an HTTP Retry-After header.
// Returns 0 when the header is absent, malformed, or a date in the past.
func ParseRetryAfterHeader(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// IsPermanentHTTPStatus reports whether an HTTP response status should cause
// the dispatcher to give up immediately (no further retries).
func IsPermanentHTTPStatus(code int) bool {
	if code == http.StatusRequestTimeout || code == http.StatusTooManyRequests {
		return false
	}
	if code >= 500 && code < 600 {
		return false
	}
	return code >= 400 && code < 500
}

// RetryWithBackoff runs op under cfg's exponential backoff, unwrapping any
// *backoff.PermanentError so callers see the underlying cause without the
// wrapper appearing in the error chain or log line.
func RetryWithBackoff(ctx context.Context, cfg BackoffConfig, op func() error) error {
	bo := cfg.NewExponential()
	bo.Reset()
	if err := backoff.Retry(op, backoff.WithContext(bo, ctx)); err != nil {
		var perm *backoff.PermanentError
		if errors.As(err, &perm) {
			return perm.Err
		}
		return err
	}
	return nil
}

// Redact replaces every occurrence of secret in s with "[redacted]". The
// empty-secret guard prevents accidental full-string replacement when a
// channel has no secret configured (e.g. auth-less SMTP relays).
func Redact(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "[redacted]")
}
