// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// A parallel burst from one IP must not slip past the lockout: with the
// handler slow enough that every request would pass the check before the
// first 401 is recorded, serializePerIP still holds it to exactly maxFailures.
func TestSerializePerIP_BurstCannotOutrunFailureLimiter(t *testing.T) {
	const maxFailures = 3
	var handled atomic.Int32
	h := serializePerIP(failureLimiter(maxFailures, time.Minute, "locked")(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			handled.Add(1)
			time.Sleep(5 * time.Millisecond)
			w.WriteHeader(http.StatusUnauthorized)
		})))

	var wg sync.WaitGroup
	for range 10 * maxFailures {
		wg.Go(func() {
			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req.RemoteAddr = "203.0.113.9:1"
			h.ServeHTTP(httptest.NewRecorder(), req)
		})
	}
	wg.Wait()

	assert.EqualValues(t, maxFailures, handled.Load())
}
