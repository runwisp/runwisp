// SPDX-FileCopyrightText: PoppyCake, s.r.o.
// SPDX-License-Identifier: GPL-3.0-or-later

package server

import (
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/httprate"
)

// tokenMaxFailures rejected tokens per client IP within tokenFailureWindow
// lock that IP out until the window slides. Only rejections count, so a caller
// with a valid token never gets throttled; the cap exists to shut down scanners
// and runaway misconfigured callers. Hook tokens and API tokens each get their
// own failureLimiter with these numbers.
const (
	tokenMaxFailures   = 20
	tokenFailureWindow = time.Minute
)

// failureLimiter counts rejected credentials per client IP. Once an IP reaches
// tokenMaxFailures in the window it is refused, even with a valid credential,
// otherwise the lockout wouldn't stop guessing. The client IP is r.RemoteAddr,
// which resolveClientIP has already rewritten when a trusted proxy relayed it.
type failureLimiter struct {
	rl   *httprate.RateLimiter
	what string // the credential, for the 429 body and the log line
}

func newFailureLimiter(what string) *failureLimiter {
	return &failureLimiter{rl: httprate.NewRateLimiter(tokenMaxFailures, tokenFailureWindow), what: what}
}

func failureKey(r *http.Request) string {
	return httprate.CanonicalizeIP(hostFromAddr(r.RemoteAddr))
}

// failures is the IP's failure count as the counter itself sees it: the
// sliding-window rate rounded to a whole number. OnLimit stops incrementing
// once the rounded rate reaches the cap, so comparing the raw rate here would
// leave a band just under the cap (19.5 to 20) where neither refuse nor fail
// takes effect and every attempt is free.
func (l *failureLimiter) failures(key string) float64 {
	_, rate, err := l.rl.Status(key)
	if err != nil {
		return 0
	}
	return math.Round(rate)
}

// refuse answers 429 and reports true when r's IP is locked out.
func (l *failureLimiter) refuse(w http.ResponseWriter, r *http.Request) bool {
	if l.failures(failureKey(r)) < tokenMaxFailures {
		return false
	}
	w.Header().Set("Retry-After", strconv.Itoa(int(tokenFailureWindow.Seconds())))
	http.Error(w, "Too many failed "+l.what+" attempts", http.StatusTooManyRequests)
	return true
}

// fail counts one rejected credential from r's IP, after the response is
// written: OnLimit is the counter's only public increment, and the headers it
// sets are moot by then. The failure that locks the IP out is logged, so one
// lockout is one line; the credential itself is never logged.
func (l *failureLimiter) fail(w http.ResponseWriter, r *http.Request) {
	key := failureKey(r)
	before := l.failures(key)
	l.rl.OnLimit(w, r, key)
	if before < tokenMaxFailures && l.failures(key) >= tokenMaxFailures {
		slog.Warn("Too many failed "+l.what+" attempts; refusing this client's "+l.what+"s for a while",
			"client", key, "failures", tokenMaxFailures, "window", tokenFailureWindow)
	}
}
