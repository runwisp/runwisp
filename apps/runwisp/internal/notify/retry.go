// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

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

// IsZero reports whether c is the zero value (no fields set). Callers use this
// to decide whether to substitute DefaultBackoff for an unset config.
func (c BackoffConfig) IsZero() bool {
	return c.InitialInterval == 0 && c.MaxInterval == 0 && c.MaxElapsedTime == 0 && c.Multiplier == 0
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

// clampRetryAfter bounds a remote-supplied Retry-After / retry_after delay so a
// hostile or misconfigured endpoint cannot stall a channel worker (and every
// notification queued behind it) for an arbitrary duration. The honored delay
// is capped at MaxInterval when set, otherwise MaxElapsedTime — keeping a 429
// backoff within the same budget the exponential backoff already enforces.
func clampRetryAfter(d time.Duration, cfg BackoffConfig) time.Duration {
	limit := cfg.MaxInterval
	if limit <= 0 {
		limit = cfg.MaxElapsedTime
	}
	if limit > 0 && d > limit {
		return limit
	}
	return d
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

// redactedError wraps err so Error() has secrets redacted from its message
// while Unwrap() still exposes the original error for errors.Is/As.
type redactedError struct {
	err    error
	secret string
}

func (r *redactedError) Error() string { return Redact(r.err.Error(), r.secret) }
func (r *redactedError) Unwrap() error { return r.err }

// RedactError is the %w-safe replacement for
// `fmt.Errorf("%s: %s", ..., Redact(err.Error(), secret))`. Using %s on
// err.Error() breaks the Unwrap() chain, which hides context.Canceled /
// context.DeadlineExceeded from errors.Is during shutdown. Wrap with %w and
// this type instead so the chain survives while the message stays redacted.
func RedactError(err error, secret string) error { return &redactedError{err: err, secret: secret} }
