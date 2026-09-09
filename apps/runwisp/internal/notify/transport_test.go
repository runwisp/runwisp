// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package notify

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHTTPProvider_RateLimitDoesNotDoubleWait guards against a regression
// where a 429 response makes the retry loop wait Retry-After PLUS the
// backoff library's own independently-computed exponential interval on top
// (see SetNextRetryInterval / rateLimitAwareBackOff). The backoff config
// here uses a 1s InitialInterval specifically so a doubled wait (at least
// ~600ms even with -50% jitter) is unmistakably distinguishable from a
// single ~100ms Retry-After wait — a loose "less than some generous
// ceiling" assertion would not catch this regression, so the upper bound is
// set well inside the doubled-wait floor.
func TestHTTPProvider_RateLimitDoesNotDoubleWait(t *testing.T) {
	const retryAfter = 100 * time.Millisecond
	var hits atomic.Int32
	start := time.Now()
	var secondAttemptAt atomic.Int64 // nanoseconds since start

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		secondAttemptAt.Store(int64(time.Since(start)))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := &HTTPProvider{
		Client: &http.Client{Timeout: 2 * time.Second},
		Backoff: BackoffConfig{
			InitialInterval: time.Second,
			MaxInterval:     2 * time.Second,
			MaxElapsedTime:  5 * time.Second,
			Multiplier:      2,
		},
		Body429Fn: func([]byte) time.Duration { return retryAfter },
	}

	require.NoError(t, p.PostJSON(context.Background(), srv.URL, "application/json", []byte("{}")))
	require.EqualValues(t, 2, hits.Load())

	elapsed := time.Duration(secondAttemptAt.Load())
	assert.Greater(t, elapsed, retryAfter-50*time.Millisecond, "should wait roughly the Retry-After delay")
	assert.Less(t, elapsed, 400*time.Millisecond,
		"second attempt should follow the Retry-After delay alone, not Retry-After plus the backoff library's own ~1s interval")
}

// TestHTTPProvider_RateLimitRespectsContextCancel ensures the single wait
// that now happens inside the backoff library's own retry loop (instead of
// a manual time.After in handleRateLimit) still returns promptly on context
// cancellation rather than waiting out the full Retry-After delay.
func TestHTTPProvider_RateLimitRespectsContextCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	p := &HTTPProvider{
		Client: &http.Client{Timeout: 2 * time.Second},
		Backoff: BackoffConfig{
			InitialInterval: time.Second,
			MaxInterval:     time.Second,
			MaxElapsedTime:  5 * time.Second,
			Multiplier:      2,
		},
		Body429Fn: func([]byte) time.Duration { return 10 * time.Second },
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := p.PostJSON(ctx, srv.URL, "application/json", []byte("{}"))
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, elapsed, 500*time.Millisecond, "should return promptly on ctx cancel, not wait out the full Retry-After")
}

// TestHTTPProvider_RateLimitZeroDelayFallsBackToNormalBackoff covers the
// d == 0 branch of handleRateLimit (no Retry-After header, no Body429Fn hit):
// the fix must not set an override in that case, leaving the library's
// normal exponential backoff in control of the retry.
func TestHTTPProvider_RateLimitZeroDelayFallsBackToNormalBackoff(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := &HTTPProvider{
		Client: &http.Client{Timeout: 2 * time.Second},
		Backoff: BackoffConfig{
			InitialInterval: 10 * time.Millisecond,
			MaxInterval:     50 * time.Millisecond,
			MaxElapsedTime:  time.Second,
			Multiplier:      2,
		},
	}

	require.NoError(t, p.PostJSON(context.Background(), srv.URL, "application/json", []byte("{}")))
	assert.EqualValues(t, 2, hits.Load())
}
