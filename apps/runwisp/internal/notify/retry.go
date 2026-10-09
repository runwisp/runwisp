// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package notify

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/runwisp/runwisp/apps/runwisp/internal/backoff"
)

// BackoffConfig controls outbound retry behavior. A zero field falls back to
// DefaultBackoff's value for it.
type BackoffConfig struct {
	InitialInterval time.Duration
	MaxInterval     time.Duration
	MaxElapsedTime  time.Duration
	Multiplier      float64
}

// DefaultBackoff is 1s → 60s per attempt with a 5m total budget.
func DefaultBackoff() BackoffConfig {
	return BackoffConfig{
		InitialInterval: time.Second,
		MaxInterval:     time.Minute,
		MaxElapsedTime:  5 * time.Minute,
		Multiplier:      2.0,
	}
}

// orDefaults fills every unset field from DefaultBackoff, so an omitted knob
// never means "retry forever".
func (c BackoffConfig) orDefaults() BackoffConfig {
	d := DefaultBackoff()
	return BackoffConfig{
		InitialInterval: cmp.Or(c.InitialInterval, d.InitialInterval),
		MaxInterval:     cmp.Or(c.MaxInterval, d.MaxInterval),
		MaxElapsedTime:  cmp.Or(c.MaxElapsedTime, d.MaxElapsedTime),
		Multiplier:      cmp.Or(c.Multiplier, d.Multiplier),
	}
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
	return code >= 400 && code < 500 &&
		code != http.StatusRequestTimeout && code != http.StatusTooManyRequests
}

// permanentError marks an error RetryWithBackoff must not retry.
type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }

// Permanent wraps err so RetryWithBackoff gives up immediately and returns err
// itself (without the wrapper).
func Permanent(err error) error { return &permanentError{err: err} }

// retryOverrideKey is the context.Value key RetryWithBackoff uses to expose
// its next-delay override to op, so op can call SetNextRetryInterval.
type retryOverrideKey struct{}

// SetNextRetryInterval overrides the delay RetryWithBackoff's retry loop
// waits before its next call to op, using the ctx that RetryWithBackoff
// passed into op. Intended for a server-supplied delay (e.g. HTTP 429
// Retry-After) that op has already decided to honor: the loop then waits
// exactly that long, once, instead of its own exponential interval. d <= 0, or
// a ctx not sourced from RetryWithBackoff, is a no-op.
func SetNextRetryInterval(ctx context.Context, d time.Duration) {
	if override, ok := ctx.Value(retryOverrideKey{}).(*time.Duration); ok && d > 0 {
		*override = d
	}
}

// stopRetriesKey is the context.Value key for the channel withStopRetries
// attaches; once it's closed, RetryWithBackoff stops scheduling retries.
type stopRetriesKey struct{}

// withStopRetries returns ctx carrying stop. After stop closes,
// RetryWithBackoff still lets the attempt in flight finish but gives up instead
// of waiting to retry, so a dead endpoint can't hold Service.Stop for its whole
// deadline.
func withStopRetries(ctx context.Context, stop <-chan struct{}) context.Context {
	return context.WithValue(ctx, stopRetriesKey{}, stop)
}

// RetryWithBackoff runs op until it succeeds, returns a Permanent error, ctx
// ends, retries are stopped (see withStopRetries), or the next wait would
// exceed cfg.MaxElapsedTime. Waits follow cfg's
// exponential curve with ±50% jitter. op is called with a context derived from
// ctx that carries the loop's override slot (see SetNextRetryInterval).
func RetryWithBackoff(ctx context.Context, cfg BackoffConfig, op func(ctx context.Context) error) error {
	cfg = cfg.orDefaults()
	curve := backoff.Exponential{Initial: cfg.InitialInterval, Max: cfg.MaxInterval, Multiplier: cfg.Multiplier, Jitter: 0.5}
	stop, _ := ctx.Value(stopRetriesKey{}).(<-chan struct{}) // nil blocks forever
	var override time.Duration
	rctx := context.WithValue(ctx, retryOverrideKey{}, &override)
	start := time.Now()
	for {
		err := op(rctx)
		if err == nil {
			return nil
		}
		if perm, ok := errors.AsType[*permanentError](err); ok {
			return perm.err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		delay := curve.Next()
		if override > 0 {
			delay, override = override, 0
		}
		if time.Since(start)+delay > cfg.MaxElapsedTime {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-stop:
			return err
		case <-time.After(delay):
		}
	}
}

// redactedError wraps err so Error() has secrets redacted from its message
// while Unwrap() still exposes the original error for errors.Is/As.
type redactedError struct {
	err    error
	secret string
}

// Error replaces every occurrence of the secret with "[redacted]". An empty
// secret (e.g. an auth-less SMTP relay) leaves the message unchanged.
func (r *redactedError) Error() string {
	if r.secret == "" {
		return r.err.Error()
	}
	return strings.ReplaceAll(r.err.Error(), r.secret, "[redacted]")
}
func (r *redactedError) Unwrap() error { return r.err }

// RedactError redacts secret from err's message while keeping the Unwrap
// chain, so errors.Is still sees context.Canceled / DeadlineExceeded during
// shutdown.
func RedactError(err error, secret string) error { return &redactedError{err: err, secret: secret} }
