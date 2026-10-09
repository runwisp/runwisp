// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package notify

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRetryWithBackoff_RateLimitOverrideHonorsMaxElapsedTime pins that a server
// that returns a Retry-After delay on every attempt (simulated by op always
// calling SetNextRetryInterval) still gives up after MaxElapsedTime, even
// though the override replaces the exponential delay.
func TestRetryWithBackoff_RateLimitOverrideHonorsMaxElapsedTime(t *testing.T) {
	cfg := BackoffConfig{
		InitialInterval: time.Millisecond,
		MaxInterval:     time.Millisecond,
		MaxElapsedTime:  50 * time.Millisecond,
		Multiplier:      2.0,
	}
	// A generous deadline: if the budget is honored the op error returns well
	// before this fires; if it's bypassed the loop only stops when this cancels.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	sentinel := errors.New("rate-limited")
	start := time.Now()
	err := RetryWithBackoff(ctx, cfg, func(ctx context.Context) error {
		SetNextRetryInterval(ctx, time.Millisecond) // always-honored Retry-After
		return sentinel
	})

	require.ErrorIs(t, err, sentinel, "must give up with the op error, not the ctx deadline")
	assert.Less(t, time.Since(start), time.Second, "must stop near MaxElapsedTime, not run until ctx cancels")
}

func TestParseRetryAfterHeader_Empty(t *testing.T) {
	h := http.Header{}
	assert.Equal(t, time.Duration(0), ParseRetryAfterHeader(h))
}

func TestParseRetryAfterHeader_SecondsSyntax(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "30")
	got := ParseRetryAfterHeader(h)
	assert.Equal(t, 30*time.Second, got)
}

func TestParseRetryAfterHeader_ZeroSeconds(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "0")
	assert.Equal(t, time.Duration(0), ParseRetryAfterHeader(h))
}

func TestParseRetryAfterHeader_NegativeSeconds(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "-5")
	assert.Equal(t, time.Duration(0), ParseRetryAfterHeader(h))
}

func TestParseRetryAfterHeader_DateInFuture(t *testing.T) {
	future := time.Now().Add(10 * time.Minute)
	h := http.Header{}
	h.Set("Retry-After", future.UTC().Format(http.TimeFormat))
	got := ParseRetryAfterHeader(h)
	// Should be roughly 10 minutes, allow ±5s slop.
	assert.Greater(t, got, 9*time.Minute)
	assert.Less(t, got, 11*time.Minute)
}

func TestParseRetryAfterHeader_DateInPast(t *testing.T) {
	past := time.Now().Add(-10 * time.Minute)
	h := http.Header{}
	h.Set("Retry-After", past.UTC().Format(http.TimeFormat))
	assert.Equal(t, time.Duration(0), ParseRetryAfterHeader(h))
}

func TestParseRetryAfterHeader_Malformed(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "not-a-number-or-date")
	assert.Equal(t, time.Duration(0), ParseRetryAfterHeader(h))
}

func TestParseRetryAfterHeader_WhitespaceOnly(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "   ")
	assert.Equal(t, time.Duration(0), ParseRetryAfterHeader(h))
}

func TestParseRetryAfterHeader_LargeSeconds(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", strconv.Itoa(3600))
	assert.Equal(t, time.Hour, ParseRetryAfterHeader(h))
}

// TestBackoffConfig_UnsetFieldsUseDefaults pins that an omitted knob (above
// all MaxElapsedTime) falls back to DefaultBackoff instead of meaning
// "retry forever".
func TestBackoffConfig_UnsetFieldsUseDefaults(t *testing.T) {
	got := BackoffConfig{InitialInterval: time.Millisecond}.orDefaults()
	want := DefaultBackoff()
	want.InitialInterval = time.Millisecond
	assert.Equal(t, want, got)
}

func TestRetryWithBackoff_PermanentStopsAndUnwraps(t *testing.T) {
	cause := errors.New("bad request")
	calls := 0
	err := RetryWithBackoff(context.Background(), BackoffConfig{InitialInterval: time.Millisecond}, func(context.Context) error {
		calls++
		return Permanent(cause)
	})
	assert.Equal(t, cause, err)
	assert.Equal(t, 1, calls)
}

func TestIsPermanentHTTPStatus(t *testing.T) {
	for code, want := range map[int]bool{
		200: false, 302: false, 399: false,
		400: true, 401: true, 404: true, 499: true,
		408: false, 429: false,
		500: false, 503: false, 599: false, 600: false,
	} {
		assert.Equalf(t, want, IsPermanentHTTPStatus(code), "status %d", code)
	}
}

func TestClampRetryAfter(t *testing.T) {
	cfg := BackoffConfig{MaxInterval: time.Minute, MaxElapsedTime: 5 * time.Minute}

	// A hostile Retry-After is clamped to MaxInterval so it can't stall the
	// channel worker for the full remote-chosen duration.
	assert.Equal(t, time.Minute, clampRetryAfter(24*time.Hour, cfg))
	assert.Equal(t, 30*time.Second, clampRetryAfter(30*time.Second, cfg))
	assert.Equal(t, time.Duration(0), clampRetryAfter(0, cfg))

	// With no MaxInterval, fall back to MaxElapsedTime as the cap.
	only := BackoffConfig{MaxElapsedTime: 2 * time.Minute}
	assert.Equal(t, 2*time.Minute, clampRetryAfter(time.Hour, only))

	assert.Equal(t, time.Hour, clampRetryAfter(time.Hour, BackoffConfig{}))
}
