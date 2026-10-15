// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
)

// failureLimiter answers 429 to a client IP that has collected maxFailures
// 401s from the wrapped handler within the sliding window, and records every
// 401 it returns. Successful calls never count, so a caller with valid
// credentials is never throttled. A locked-out IP is refused even with valid
// credentials, otherwise the lockout wouldn't stop guessing.
func failureLimiter(maxFailures int, window time.Duration, msg string) func(http.Handler) http.Handler {
	rl := httprate.NewRateLimiter(maxFailures, window)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := limiterKey(r)
			if _, rate, err := rl.Status(key); err == nil && rate >= float64(maxFailures) {
				w.Header().Set("Retry-After", strconv.Itoa(int(window.Seconds())))
				http.Error(w, msg, http.StatusTooManyRequests)
				return
			}
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			if ww.Status() == http.StatusUnauthorized {
				// OnLimit is the counter's only public increment; the response is
				// already written, so the headers it sets are moot.
				rl.OnLimit(ww, r, key)
			}
		})
	}
}

// serializePerIP runs one request per client IP at a time. Mounted outside a
// failureLimiter, it closes the gap between the limiter's check and its 401
// bookkeeping: without it, one IP could fire a burst of guesses in parallel and
// have them all checked before the first failure is recorded. Other IPs are
// never blocked, so a slow client only stalls itself.
func serializePerIP(next http.Handler) http.Handler {
	var mu sync.Mutex
	locks := map[string]*ipLock{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := limiterKey(r)
		mu.Lock()
		l := locks[key]
		if l == nil {
			l = &ipLock{}
			locks[key] = l
		}
		l.refs++
		mu.Unlock()

		l.Lock()
		defer func() {
			l.Unlock()
			mu.Lock()
			if l.refs--; l.refs == 0 {
				delete(locks, key)
			}
			mu.Unlock()
		}()
		next.ServeHTTP(w, r)
	})
}

type ipLock struct {
	sync.Mutex
	refs int // guarded by serializePerIP's map mutex, not by the embedded one
}

func limiterKey(r *http.Request) string {
	return httprate.CanonicalizeIP(hostFromAddr(r.RemoteAddr))
}
