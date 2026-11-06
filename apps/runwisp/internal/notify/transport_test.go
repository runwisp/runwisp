// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package notify

import (
	"context"
	"io"
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
// retry loop's own independently-computed exponential interval on top
// (see SetNextRetryInterval). The backoff config
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

	require.NoError(t, p.Post(context.Background(), srv.URL, "application/json", []byte("{}"), nil))
	require.EqualValues(t, 2, hits.Load())

	elapsed := time.Duration(secondAttemptAt.Load())
	assert.Greater(t, elapsed, retryAfter-50*time.Millisecond, "should wait roughly the Retry-After delay")
	assert.Less(t, elapsed, 400*time.Millisecond,
		"second attempt should follow the Retry-After delay alone, not Retry-After plus the retry loop's own ~1s interval")
}

// TestHTTPProvider_RateLimitRespectsContextCancel ensures the single wait
// that now happens inside RetryWithBackoff (instead of
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
	err := p.Post(ctx, srv.URL, "application/json", []byte("{}"), nil)
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, elapsed, 500*time.Millisecond, "should return promptly on ctx cancel, not wait out the full Retry-After")
}

// TestHTTPProvider_RateLimitZeroDelayFallsBackToNormalBackoff covers the
// d == 0 branch of handleRateLimit (no Retry-After header, no Body429Fn hit):
// the fix must not set an override in that case, leaving the loop's
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

	require.NoError(t, p.Post(context.Background(), srv.URL, "application/json", []byte("{}"), nil))
	assert.EqualValues(t, 2, hits.Load())
}

// TestHTTPProvider_RedirectThatDropsThePostIsAFailure guards against a 301/302
// (say an http:// notifier URL that redirects to https://) being followed as a
// body-less retry: the endpoint answers that with 200, so the delivery used to
// read as a success although the notification never arrived.
func TestHTTPProvider_RedirectThatDropsThePostIsAFailure(t *testing.T) {
	var landed atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		landed.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	p := &HTTPProvider{
		Client:  &http.Client{Timeout: 2 * time.Second},
		Backoff: BackoffConfig{InitialInterval: time.Millisecond, MaxInterval: time.Millisecond, MaxElapsedTime: time.Second, Multiplier: 1},
	}

	err := p.Post(context.Background(), redirector.URL, "application/json", []byte("{}"), nil)
	require.Error(t, err, "a redirect that drops the POST must not count as delivered")
	assert.Contains(t, err.Error(), target.URL, "the error should name where it redirected")
	assert.Zero(t, landed.Load(), "the redirect target must not be hit without the body")
}

// TestHTTPProvider_PostSurvivesMethodPreservingRedirect keeps 307/308, which
// replay the POST and body, working.
func TestHTTPProvider_PostSurvivesMethodPreservingRedirect(t *testing.T) {
	var got atomic.Value
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.Store(r.Method + " " + string(b))
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusPermanentRedirect)
	}))
	defer redirector.Close()

	p := NewHTTPProvider()
	require.NoError(t, p.Post(context.Background(), redirector.URL, "application/json", []byte("{}"), nil))
	assert.Equal(t, "POST {}", got.Load())
}
