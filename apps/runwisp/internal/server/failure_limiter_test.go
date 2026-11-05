// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/httprate"
	"github.com/stretchr/testify/assert"
)

// slidingCounter reports a sliding-window failure rate just under the cap
// (between 19.5 and 20) whatever the clock says, and counts increments.
type slidingCounter struct{ increments int }

func (*slidingCounter) Config(int, time.Duration) {}

func (c *slidingCounter) Increment(key string, w time.Time) error { return c.IncrementBy(key, w, 1) }

func (c *slidingCounter) IncrementBy(string, time.Time, int) error {
	c.increments++
	return nil
}

func (*slidingCounter) Get(_ string, current, _ time.Time) (int, int, error) {
	// rate = prev*f + curr, f being the share of the previous window still in play.
	f := 1 - float64(time.Now().UTC().Sub(current))/float64(tokenFailureWindow)
	for prev := 0; prev < 1_000_000; prev++ {
		for curr := range tokenMaxFailures {
			if rate := float64(curr) + float64(prev)*f; rate > 19.55 && rate < 19.95 {
				return curr, prev, nil
			}
		}
	}
	return 0, 0, nil
}

// A client whose sliding-window rate sits between 19.5 and 20 must either be
// refused or have its failure counted. The counter rounds the rate before it
// compares, so if refuse waves the client through at 19.7 while fail's
// increment is rejected, every attempt in that band is free.
func TestFailureLimiter_RefusesOrCounts(t *testing.T) {
	counter := &slidingCounter{}
	l := &failureLimiter{
		rl:   httprate.NewRateLimiter(tokenMaxFailures, tokenFailureWindow, httprate.WithLimitCounter(counter)),
		what: "API token",
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.0.2.9:1234"
	w := httptest.NewRecorder()

	if l.refuse(w, r) {
		return
	}
	l.fail(w, r)
	assert.Equal(t, 1, counter.increments, "a failure that is not refused must be counted")
}
