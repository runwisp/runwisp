// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: Apache-2.0

package notify

import (
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

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
	// 0 is not > 0 → returns 0
	assert.Equal(t, time.Duration(0), ParseRetryAfterHeader(h))
}

func TestParseRetryAfterHeader_NegativeSeconds(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "-5")
	// negative → not > 0 → try date parse → fails → returns 0
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
